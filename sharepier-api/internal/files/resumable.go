package files

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrUploadSessionExpired = errors.New("upload session expired")
var ErrUploadOffsetMismatch = errors.New("upload offset mismatch")
var ErrUploadChunkTooLarge = errors.New("upload chunk too large")
var ErrUploadSessionNotReady = errors.New("upload session not ready")

type CreateUploadSessionParams struct {
	OwnerUserID  int64
	OriginalName string
	DisplayName  string
	ContentType  string
	TotalSize    int64
}

type UploadSession struct {
	UploadToken  string    `json:"uploadToken"`
	OriginalName string    `json:"originalName"`
	DisplayName  string    `json:"displayName"`
	ContentType  string    `json:"contentType"`
	TotalSize    int64     `json:"totalSize"`
	ReceivedSize int64     `json:"receivedSize"`
	ChunkSize    int64     `json:"chunkSize"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
	ExpiredAt    time.Time `json:"expiredAt"`
}

type uploadSessionState struct {
	UserID       int64
	UploadToken  string
	OriginalName string
	DisplayName  string
	ContentType  string
	TotalSize    int64
	ReceivedSize int64
	Status       string
	CreatedAt    time.Time
	ExpiredAt    time.Time
}

func (s *Service) CreateUploadSession(ctx context.Context, params CreateUploadSessionParams) (UploadSession, error) {
	originalName := sanitizeFileName(params.OriginalName)
	if originalName == "" {
		return UploadSession{}, fmt.Errorf("%w: missing original file name", ErrInvalidUpload)
	}
	if params.TotalSize <= 0 {
		return UploadSession{}, fmt.Errorf("%w: invalid total size", ErrInvalidUpload)
	}

	displayName := sanitizeFileName(params.DisplayName)
	if displayName == "" {
		displayName = originalName
	}

	contentType := strings.TrimSpace(params.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	uploadToken, err := randomToken(24)
	if err != nil {
		return UploadSession{}, err
	}

	if err := os.MkdirAll(s.uploadSessionDir(), 0o755); err != nil {
		return UploadSession{}, err
	}
	if file, err := os.OpenFile(s.uploadSessionPath(uploadToken), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err != nil {
		return UploadSession{}, err
	} else {
		file.Close()
	}

	createdAt := time.Now().UTC()
	expiredAt := createdAt.Add(s.uploadSessionTTL)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		_ = os.Remove(s.uploadSessionPath(uploadToken))
		return UploadSession{}, err
	}
	defer tx.Rollback()

	if err := s.lockStorageQuotaTx(ctx, tx); err != nil {
		_ = os.Remove(s.uploadSessionPath(uploadToken))
		return UploadSession{}, err
	}
	if err := s.ensureStorageQuotaTx(ctx, tx, params.TotalSize, ""); err != nil {
		_ = os.Remove(s.uploadSessionPath(uploadToken))
		return UploadSession{}, err
	}

	if _, err := tx.ExecContext(
		ctx,
		`insert into upload_sessions (
			user_id,
			upload_token,
			status,
			total_size,
			received_size,
			original_name,
			display_name,
			content_type,
			expired_at
		) values ($1, $2, 'pending', $3, 0, $4, $5, $6, $7)`,
		params.OwnerUserID,
		uploadToken,
		params.TotalSize,
		originalName,
		displayName,
		contentType,
		expiredAt,
	); err != nil {
		_ = os.Remove(s.uploadSessionPath(uploadToken))
		return UploadSession{}, err
	}
	if err := tx.Commit(); err != nil {
		_ = os.Remove(s.uploadSessionPath(uploadToken))
		return UploadSession{}, err
	}

	return UploadSession{
		UploadToken:  uploadToken,
		OriginalName: originalName,
		DisplayName:  displayName,
		ContentType:  contentType,
		TotalSize:    params.TotalSize,
		ReceivedSize: 0,
		ChunkSize:    s.resumableChunkSize,
		Status:       "pending",
		CreatedAt:    createdAt,
		ExpiredAt:    expiredAt,
	}, nil
}

func (s *Service) GetUploadSession(ctx context.Context, uploadToken string) (UploadSession, error) {
	session, err := s.loadUploadSession(ctx, uploadToken)
	if err != nil {
		return UploadSession{}, err
	}

	return s.publicUploadSession(session), nil
}

func (s *Service) AppendUploadChunk(ctx context.Context, uploadToken string, offset int64, reader io.Reader) (UploadSession, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UploadSession{}, err
	}
	defer tx.Rollback()

	session, err := s.loadUploadSessionForUpdate(ctx, tx, uploadToken)
	if err != nil {
		return UploadSession{}, err
	}
	if err := validateUploadSessionState(session, offset); err != nil {
		return UploadSession{}, err
	}

	partFile, err := os.OpenFile(s.uploadSessionPath(uploadToken), os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return UploadSession{}, err
	}
	defer partFile.Close()

	if _, err := partFile.Seek(session.ReceivedSize, io.SeekStart); err != nil {
		return UploadSession{}, err
	}

	remaining := session.TotalSize - session.ReceivedSize
	limit := s.resumableChunkSize
	if remaining < limit {
		limit = remaining
	}

	written, err := writeChunkWithLimit(partFile, reader, session.ReceivedSize, limit)
	if err != nil {
		return UploadSession{}, err
	}
	if written == 0 {
		return UploadSession{}, fmt.Errorf("%w: empty upload chunk", ErrInvalidUpload)
	}

	nextReceived := session.ReceivedSize + written
	nextStatus := "uploading"
	if nextReceived == session.TotalSize {
		nextStatus = "uploaded"
	}

	if _, err := tx.ExecContext(
		ctx,
		`update upload_sessions
		set received_size = $2, status = $3
		where upload_token = $1`,
		uploadToken,
		nextReceived,
		nextStatus,
	); err != nil {
		return UploadSession{}, err
	}

	if err := tx.Commit(); err != nil {
		return UploadSession{}, err
	}

	session.ReceivedSize = nextReceived
	session.Status = nextStatus
	return s.publicUploadSession(session), nil
}

func (s *Service) CompleteUploadSession(ctx context.Context, uploadToken string) (FileRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FileRecord{}, err
	}

	if err := s.lockStorageQuotaTx(ctx, tx); err != nil {
		tx.Rollback()
		return FileRecord{}, err
	}

	session, err := s.loadUploadSessionForUpdate(ctx, tx, uploadToken)
	if err != nil {
		tx.Rollback()
		return FileRecord{}, err
	}
	if time.Now().UTC().After(session.ExpiredAt) {
		tx.Rollback()
		return FileRecord{}, ErrUploadSessionExpired
	}
	if session.ReceivedSize != session.TotalSize {
		tx.Rollback()
		return FileRecord{}, ErrUploadSessionNotReady
	}
	if err := s.ensureStorageQuotaTx(ctx, tx, session.TotalSize, session.UploadToken); err != nil {
		tx.Rollback()
		return FileRecord{}, err
	}

	if _, err := tx.ExecContext(
		ctx,
		`update upload_sessions set status = 'completing' where upload_token = $1`,
		uploadToken,
	); err != nil {
		tx.Rollback()
		return FileRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return FileRecord{}, err
	}

	partFile, err := os.Open(s.uploadSessionPath(uploadToken))
	if err != nil {
		return FileRecord{}, err
	}
	defer partFile.Close()

	record, err := s.Upload(ctx, UploadParams{
		OwnerUserID:           session.UserID,
		OriginalName:          session.OriginalName,
		DisplayName:           session.DisplayName,
		ExpectedSize:          session.TotalSize,
		QuotaReservationToken: session.UploadToken,
		Reader:                partFile,
	})
	if err != nil {
		_, _ = s.db.ExecContext(ctx, `update upload_sessions set status = 'uploaded' where upload_token = $1`, uploadToken)
		return FileRecord{}, err
	}

	if _, err := s.db.ExecContext(ctx, `delete from upload_sessions where upload_token = $1`, uploadToken); err != nil {
		return FileRecord{}, err
	}
	_ = os.Remove(s.uploadSessionPath(uploadToken))

	return record, nil
}

func (s *Service) loadUploadSession(ctx context.Context, uploadToken string) (uploadSessionState, error) {
	var session uploadSessionState
	err := s.db.QueryRowContext(
		ctx,
		`select
			coalesce(user_id, 0),
			upload_token,
			original_name,
			display_name,
			content_type,
			total_size,
			received_size,
			status,
			created_at,
			expired_at
		from upload_sessions
		where upload_token = $1`,
		uploadToken,
	).Scan(
		&session.UserID,
		&session.UploadToken,
		&session.OriginalName,
		&session.DisplayName,
		&session.ContentType,
		&session.TotalSize,
		&session.ReceivedSize,
		&session.Status,
		&session.CreatedAt,
		&session.ExpiredAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return uploadSessionState{}, ErrNotFound
		}
		return uploadSessionState{}, err
	}
	if time.Now().UTC().After(session.ExpiredAt) {
		return uploadSessionState{}, ErrUploadSessionExpired
	}

	return session, nil
}

func (s *Service) loadUploadSessionForUpdate(ctx context.Context, tx *sql.Tx, uploadToken string) (uploadSessionState, error) {
	var session uploadSessionState
	err := tx.QueryRowContext(
		ctx,
		`select
			coalesce(user_id, 0),
			upload_token,
			original_name,
			display_name,
			content_type,
			total_size,
			received_size,
			status,
			created_at,
			expired_at
		from upload_sessions
		where upload_token = $1
		for update`,
		uploadToken,
	).Scan(
		&session.UserID,
		&session.UploadToken,
		&session.OriginalName,
		&session.DisplayName,
		&session.ContentType,
		&session.TotalSize,
		&session.ReceivedSize,
		&session.Status,
		&session.CreatedAt,
		&session.ExpiredAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return uploadSessionState{}, ErrNotFound
		}
		return uploadSessionState{}, err
	}

	return session, nil
}

func (s *Service) publicUploadSession(session uploadSessionState) UploadSession {
	return UploadSession{
		UploadToken:  session.UploadToken,
		OriginalName: session.OriginalName,
		DisplayName:  session.DisplayName,
		ContentType:  session.ContentType,
		TotalSize:    session.TotalSize,
		ReceivedSize: session.ReceivedSize,
		ChunkSize:    s.resumableChunkSize,
		Status:       session.Status,
		CreatedAt:    session.CreatedAt,
		ExpiredAt:    session.ExpiredAt,
	}
}

func (s *Service) uploadSessionDir() string {
	return filepath.Join(s.store.Root(), "_upload_sessions")
}

func (s *Service) uploadSessionPath(uploadToken string) string {
	return filepath.Join(s.uploadSessionDir(), fmt.Sprintf("%s.part", uploadToken))
}

func validateUploadSessionState(session uploadSessionState, offset int64) error {
	if time.Now().UTC().After(session.ExpiredAt) {
		return ErrUploadSessionExpired
	}
	if offset != session.ReceivedSize {
		return ErrUploadOffsetMismatch
	}
	if session.Status == "completing" {
		return ErrUploadSessionNotReady
	}
	if session.ReceivedSize >= session.TotalSize {
		return ErrUploadSessionNotReady
	}

	return nil
}

func writeChunkWithLimit(file *os.File, reader io.Reader, currentOffset, limit int64) (int64, error) {
	limitedReader := io.LimitReader(reader, limit+1)
	written, err := io.Copy(file, limitedReader)
	if err != nil {
		_ = file.Truncate(currentOffset)
		_, _ = file.Seek(currentOffset, io.SeekStart)
		return 0, err
	}
	if written > limit {
		_ = file.Truncate(currentOffset)
		_, _ = file.Seek(currentOffset, io.SeekStart)
		return 0, ErrUploadChunkTooLarge
	}

	return written, nil
}
