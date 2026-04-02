package files

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sharepier-api/internal/storage"
)

var ErrInvalidUpload = errors.New("invalid upload")
var ErrNotFound = errors.New("file not found")
var ErrInvalidStatus = errors.New("invalid file status")
var ErrInvalidSelection = errors.New("invalid file selection")

type Service struct {
	db                 *sql.DB
	store              *storage.LocalFSStore
	publicBaseURL      string
	resumableChunkSize int64
	uploadSessionTTL   time.Duration
}

type UploadParams struct {
	OwnerUserID  int64
	OriginalName string
	DisplayName  string
	Reader       io.Reader
}

type FileRecord struct {
	ID            int64     `json:"id"`
	PublicID      string    `json:"publicId"`
	OriginalName  string    `json:"originalName"`
	DisplayName   string    `json:"displayName"`
	Status        string    `json:"status"`
	Visibility    string    `json:"visibility"`
	ContentType   string    `json:"contentType"`
	Size          int64     `json:"size"`
	DownloadCount int64     `json:"downloadCount"`
	DownloadURL   string    `json:"downloadUrl"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type PublicFileRecord struct {
	PublicID      string    `json:"publicId"`
	OriginalName  string    `json:"originalName"`
	DisplayName   string    `json:"displayName"`
	ContentType   string    `json:"contentType"`
	Size          int64     `json:"size"`
	DownloadCount int64     `json:"downloadCount"`
	SHA256        string    `json:"sha256"`
	DownloadURL   string    `json:"downloadUrl"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type PublicDownload struct {
	FileID      int64
	StorageKey  string
	FileName    string
	ContentType string
	Size        int64
	SHA256      string
	UpdatedAt   time.Time
}

func NewService(
	database *sql.DB,
	store *storage.LocalFSStore,
	publicBaseURL string,
	resumableChunkSize int64,
	uploadSessionTTL time.Duration,
) *Service {
	if resumableChunkSize <= 0 {
		resumableChunkSize = 8 << 20
	}
	if uploadSessionTTL <= 0 {
		uploadSessionTTL = 24 * time.Hour
	}

	return &Service{
		db:                 database,
		store:              store,
		publicBaseURL:      strings.TrimRight(publicBaseURL, "/"),
		resumableChunkSize: resumableChunkSize,
		uploadSessionTTL:   uploadSessionTTL,
	}
}

func (s *Service) Upload(ctx context.Context, params UploadParams) (FileRecord, error) {
	originalName := sanitizeFileName(params.OriginalName)
	if originalName == "" {
		return FileRecord{}, fmt.Errorf("%w: missing original file name", ErrInvalidUpload)
	}

	displayName := sanitizeFileName(params.DisplayName)
	if displayName == "" {
		displayName = originalName
	}

	tempFile, err := os.CreateTemp("", "sharepier-upload-*")
	if err != nil {
		return FileRecord{}, err
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()

	hasher := sha256.New()
	writer := io.MultiWriter(tempFile, hasher)
	buffer := make([]byte, 32*1024)
	sniff := make([]byte, 0, 512)
	var totalSize int64

	for {
		n, readErr := params.Reader.Read(buffer)
		if n > 0 {
			if len(sniff) < 512 {
				remaining := 512 - len(sniff)
				if remaining > n {
					remaining = n
				}
				sniff = append(sniff, buffer[:remaining]...)
			}

			written, writeErr := writer.Write(buffer[:n])
			if writeErr != nil {
				tempFile.Close()
				return FileRecord{}, writeErr
			}
			totalSize += int64(written)
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			tempFile.Close()
			return FileRecord{}, readErr
		}
	}

	if err := tempFile.Close(); err != nil {
		return FileRecord{}, err
	}

	contentType := http.DetectContentType(sniff)
	if totalSize == 0 {
		contentType = "application/octet-stream"
	}

	publicID, err := randomToken(12)
	if err != nil {
		return FileRecord{}, err
	}
	storageKey, err := buildStorageKey(displayName)
	if err != nil {
		return FileRecord{}, err
	}
	sha256Hex := hex.EncodeToString(hasher.Sum(nil))

	if err := s.store.PutFile(tempPath, storageKey); err != nil {
		return FileRecord{}, err
	}

	record, err := s.insertUpload(ctx, uploadInsertParams{
		OwnerUserID:  params.OwnerUserID,
		StorageKey:   storageKey,
		SHA256:       sha256Hex,
		Size:         totalSize,
		ContentType:  contentType,
		OriginalName: originalName,
		DisplayName:  displayName,
		PublicID:     publicID,
	})
	if err != nil {
		_ = s.store.Delete(storageKey)
		return FileRecord{}, err
	}

	return record, nil
}

func (s *Service) List(ctx context.Context) ([]FileRecord, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`select
			f.id,
			f.public_id,
			f.original_name,
			f.display_name,
			f.status,
			f.visibility,
			o.content_type,
			o.size,
			f.download_count,
			f.created_at,
			f.updated_at
		from files f
		join objects o on o.id = f.object_id
		order by f.created_at desc`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]FileRecord, 0)
	for rows.Next() {
		var item FileRecord
		if err := rows.Scan(
			&item.ID,
			&item.PublicID,
			&item.OriginalName,
			&item.DisplayName,
			&item.Status,
			&item.Visibility,
			&item.ContentType,
			&item.Size,
			&item.DownloadCount,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		item.DownloadURL = s.downloadURL(item.PublicID, item.DisplayName)
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func (s *Service) SetStatus(ctx context.Context, fileID int64, status string) (FileRecord, error) {
	status = strings.TrimSpace(strings.ToLower(status))
	switch status {
	case "active", "disabled":
	default:
		return FileRecord{}, ErrInvalidStatus
	}

	var item FileRecord
	err := s.db.QueryRowContext(
		ctx,
		`update files as f
		set status = $2, updated_at = now()
		from objects as o
		where f.object_id = o.id and f.id = $1
		returning
			f.id,
			f.public_id,
			f.original_name,
			f.display_name,
			f.status,
			f.visibility,
			o.content_type,
			o.size,
			f.download_count,
			f.created_at,
			f.updated_at`,
		fileID,
		status,
	).Scan(
		&item.ID,
		&item.PublicID,
		&item.OriginalName,
		&item.DisplayName,
		&item.Status,
		&item.Visibility,
		&item.ContentType,
		&item.Size,
		&item.DownloadCount,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FileRecord{}, ErrNotFound
		}
		return FileRecord{}, err
	}

	item.DownloadURL = s.downloadURL(item.PublicID, item.DisplayName)
	return item, nil
}

func (s *Service) SetStatusMany(ctx context.Context, fileIDs []int64, status string) (int64, error) {
	status = strings.TrimSpace(strings.ToLower(status))
	switch status {
	case "active", "disabled":
	default:
		return 0, ErrInvalidStatus
	}

	normalized := normalizeFileIDs(fileIDs)
	if len(normalized) == 0 {
		return 0, ErrInvalidSelection
	}

	args := make([]any, 0, len(normalized)+1)
	args = append(args, status)
	placeholders := make([]string, 0, len(normalized))
	for _, fileID := range normalized {
		args = append(args, fileID)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}

	query := fmt.Sprintf(
		`update files
		set status = $1, updated_at = now()
		where id in (%s)`,
		strings.Join(placeholders, ", "),
	)

	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}

	affectedCount, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if affectedCount == 0 {
		return 0, ErrNotFound
	}

	return affectedCount, nil
}

func (s *Service) Delete(ctx context.Context, fileID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var objectID int64
	var storageKey string
	if err := tx.QueryRowContext(
		ctx,
		`select o.id, o.storage_key
		from files as f
		join objects as o on o.id = f.object_id
		where f.id = $1`,
		fileID,
	).Scan(&objectID, &storageKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	if _, err := tx.ExecContext(ctx, `delete from files where id = $1`, fileID); err != nil {
		return err
	}

	var remaining int
	if err := tx.QueryRowContext(ctx, `select count(*) from files where object_id = $1`, objectID).Scan(&remaining); err != nil {
		return err
	}

	shouldDeleteObject := remaining == 0
	if shouldDeleteObject {
		if _, err := tx.ExecContext(ctx, `delete from objects where id = $1`, objectID); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	if shouldDeleteObject {
		_ = s.store.Delete(storageKey)
	}

	return nil
}

func (s *Service) DeleteMany(ctx context.Context, fileIDs []int64) (int64, error) {
	normalized := normalizeFileIDs(fileIDs)
	if len(normalized) == 0 {
		return 0, ErrInvalidSelection
	}

	var affectedCount int64
	for _, fileID := range normalized {
		if err := s.Delete(ctx, fileID); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return affectedCount, err
		}

		affectedCount += 1
	}

	if affectedCount == 0 {
		return 0, ErrNotFound
	}

	return affectedCount, nil
}

func (s *Service) GetPublicDownload(ctx context.Context, publicID string) (PublicDownload, error) {
	var result PublicDownload
	err := s.db.QueryRowContext(
		ctx,
		`select
			f.id,
			o.storage_key,
			f.display_name,
			o.content_type,
			o.size,
			o.sha256,
			f.updated_at
		from files f
		join objects o on o.id = f.object_id
		where f.public_id = $1 and f.status = 'active' and f.visibility = 'public'`,
		publicID,
	).Scan(
		&result.FileID,
		&result.StorageKey,
		&result.FileName,
		&result.ContentType,
		&result.Size,
		&result.SHA256,
		&result.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PublicDownload{}, ErrNotFound
		}
		return PublicDownload{}, err
	}

	return result, nil
}

func (s *Service) GetPublicFile(ctx context.Context, publicID string) (PublicFileRecord, error) {
	var item PublicFileRecord
	err := s.db.QueryRowContext(
		ctx,
		`select
			f.public_id,
			f.original_name,
			f.display_name,
			o.content_type,
			o.size,
			f.download_count,
			o.sha256,
			f.created_at,
			f.updated_at
		from files as f
		join objects as o on o.id = f.object_id
		where f.public_id = $1 and f.status = 'active' and f.visibility = 'public'`,
		publicID,
	).Scan(
		&item.PublicID,
		&item.OriginalName,
		&item.DisplayName,
		&item.ContentType,
		&item.Size,
		&item.DownloadCount,
		&item.SHA256,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PublicFileRecord{}, ErrNotFound
		}
		return PublicFileRecord{}, err
	}

	item.DownloadURL = s.downloadURL(item.PublicID, item.DisplayName)
	return item, nil
}

func (s *Service) RecordDownload(ctx context.Context, fileID int64, ipAddress, userAgent, referer string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `update files set download_count = download_count + 1 where id = $1`, fileID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(
		ctx,
		`insert into download_events (file_id, ip_hash, user_agent, referer) values ($1, $2, $3, $4)`,
		fileID,
		hashString(ipAddress),
		userAgent,
		referer,
	); err != nil {
		return err
	}

	return tx.Commit()
}

type uploadInsertParams struct {
	OwnerUserID  int64
	StorageKey   string
	SHA256       string
	Size         int64
	ContentType  string
	OriginalName string
	DisplayName  string
	PublicID     string
}

func (s *Service) insertUpload(ctx context.Context, params uploadInsertParams) (FileRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FileRecord{}, err
	}
	defer tx.Rollback()

	var objectID int64
	if err := tx.QueryRowContext(
		ctx,
		`insert into objects (storage_key, sha256, size, content_type)
		 values ($1, $2, $3, $4)
		 returning id`,
		params.StorageKey,
		params.SHA256,
		params.Size,
		params.ContentType,
	).Scan(&objectID); err != nil {
		return FileRecord{}, err
	}

	var record FileRecord
	if err := tx.QueryRowContext(
		ctx,
		`insert into files (
			object_id,
			owner_user_id,
			original_name,
			display_name,
			public_id,
			visibility,
			status
		) values ($1, $2, $3, $4, $5, 'public', 'active')
		returning id, public_id, original_name, display_name, status, visibility, download_count, created_at, updated_at`,
		objectID,
		params.OwnerUserID,
		params.OriginalName,
		params.DisplayName,
		params.PublicID,
	).Scan(
		&record.ID,
		&record.PublicID,
		&record.OriginalName,
		&record.DisplayName,
		&record.Status,
		&record.Visibility,
		&record.DownloadCount,
		&record.CreatedAt,
		&record.UpdatedAt,
	); err != nil {
		return FileRecord{}, err
	}

	record.ContentType = params.ContentType
	record.Size = params.Size
	record.DownloadURL = s.downloadURL(record.PublicID, record.DisplayName)

	if err := tx.Commit(); err != nil {
		return FileRecord{}, err
	}

	return record, nil
}

func (s *Service) downloadURL(publicID, displayName string) string {
	escapedName := url.PathEscape(displayName)
	return fmt.Sprintf("%s/f/%s/%s", s.publicBaseURL, publicID, escapedName)
}

func sanitizeFileName(value string) string {
	value = strings.TrimSpace(filepath.Base(value))
	if value == "." || value == string(filepath.Separator) {
		return ""
	}

	value = strings.Map(func(r rune) rune {
		switch r {
		case 0, '/', '\\':
			return -1
		default:
			return r
		}
	}, value)

	return strings.TrimSpace(value)
}

func buildStorageKey(name string) (string, error) {
	token, err := randomToken(24)
	if err != nil {
		return "", err
	}

	extension := strings.ToLower(filepath.Ext(name))
	if len(extension) > 16 {
		extension = extension[:16]
	}

	now := time.Now().UTC()
	return fmt.Sprintf("%04d/%02d/%02d/%s%s", now.Year(), now.Month(), now.Day(), token, extension), nil
}

func randomToken(characters int) (string, error) {
	rawSize := characters
	if rawSize < 8 {
		rawSize = 8
	}

	bytes := make([]byte, rawSize)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	token := base64.RawURLEncoding.EncodeToString(bytes)
	if len(token) > characters {
		return token[:characters], nil
	}

	return token, nil
}

func hashString(value string) string {
	if value == "" {
		return ""
	}

	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func normalizeFileIDs(fileIDs []int64) []int64 {
	if len(fileIDs) == 0 {
		return nil
	}

	seen := make(map[int64]struct{}, len(fileIDs))
	items := make([]int64, 0, len(fileIDs))
	for _, fileID := range fileIDs {
		if fileID <= 0 {
			continue
		}
		if _, exists := seen[fileID]; exists {
			continue
		}

		seen[fileID] = struct{}{}
		items = append(items, fileID)
	}

	return items
}
