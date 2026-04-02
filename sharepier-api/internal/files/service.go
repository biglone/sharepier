package files

import (
	"context"
	"crypto/hmac"
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
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"sharepier-api/internal/storage"
)

var ErrInvalidUpload = errors.New("invalid upload")
var ErrStorageQuotaExceeded = errors.New("storage quota exceeded")
var ErrNotFound = errors.New("file not found")
var ErrInvalidStatus = errors.New("invalid file status")
var ErrInvalidSelection = errors.New("invalid file selection")
var ErrInvalidSharePolicy = errors.New("invalid share policy")
var ErrShareExpired = errors.New("share expired")
var ErrSharePasswordRequired = errors.New("share password required")
var ErrSharePasswordInvalid = errors.New("share password invalid")
var ErrShareDownloadLimitReached = errors.New("share download limit reached")

type Service struct {
	db                 *sql.DB
	store              storage.Store
	publicBaseURL      string
	storageQuotaBytes  int64
	resumableChunkSize int64
	uploadSessionTTL   time.Duration
	shareAccessSecret  string
	shareAccessTTL     time.Duration
}

type StorageStats struct {
	TotalFiles     int64      `json:"totalFiles"`
	ActiveFiles    int64      `json:"activeFiles"`
	DisabledFiles  int64      `json:"disabledFiles"`
	TotalDownloads int64      `json:"totalDownloads"`
	ObjectCount    int64      `json:"objectCount"`
	StoredBytes    int64      `json:"storedBytes"`
	LatestObjectAt *time.Time `json:"latestObjectAt,omitempty"`
}

type UploadParams struct {
	OwnerUserID           int64
	OriginalName          string
	DisplayName           string
	ExpectedSize          int64
	QuotaReservationToken string
	Reader                io.Reader
}

type FileRecord struct {
	ID                 int64      `json:"id"`
	PublicID           string     `json:"publicId"`
	OriginalName       string     `json:"originalName"`
	DisplayName        string     `json:"displayName"`
	Status             string     `json:"status"`
	Visibility         string     `json:"visibility"`
	ContentType        string     `json:"contentType"`
	Size               int64      `json:"size"`
	DownloadCount      int64      `json:"downloadCount"`
	DownloadURL        string     `json:"downloadUrl"`
	ExpiresAt          *time.Time `json:"expiresAt,omitempty"`
	MaxDownloads       *int64     `json:"maxDownloads,omitempty"`
	RemainingDownloads *int64     `json:"remainingDownloads,omitempty"`
	PasswordProtected  bool       `json:"passwordProtected"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

type PublicFileRecord struct {
	PublicID           string     `json:"publicId"`
	OriginalName       string     `json:"originalName"`
	DisplayName        string     `json:"displayName"`
	ContentType        string     `json:"contentType"`
	Size               int64      `json:"size"`
	DownloadCount      int64      `json:"downloadCount"`
	SHA256             string     `json:"sha256"`
	DownloadURL        string     `json:"downloadUrl"`
	ExpiresAt          *time.Time `json:"expiresAt,omitempty"`
	MaxDownloads       *int64     `json:"maxDownloads,omitempty"`
	RemainingDownloads *int64     `json:"remainingDownloads,omitempty"`
	PasswordProtected  bool       `json:"passwordProtected"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

type PublicDownload struct {
	FileID      int64
	PublicID    string
	StorageKey  string
	FileName    string
	ContentType string
	Size        int64
	SHA256      string
	UpdatedAt   time.Time
}

type SharePolicyUpdateParams struct {
	ExpiresAt    *time.Time
	MaxDownloads *int64
	PasswordMode string
	Password     string
}

type ShareAccessGrant struct {
	Token     string
	ExpiresAt time.Time
}

type publicAccessState struct {
	FileID             int64
	PublicID           string
	OriginalName       string
	DisplayName        string
	Status             string
	Visibility         string
	ContentType        string
	Size               int64
	DownloadCount      int64
	SHA256             string
	StorageKey         string
	ExpiresAt          sql.NullTime
	MaxDownloads       sql.NullInt64
	AccessPasswordHash sql.NullString
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func NewService(
	database *sql.DB,
	store storage.Store,
	publicBaseURL string,
	storageQuotaBytes int64,
	resumableChunkSize int64,
	uploadSessionTTL time.Duration,
	shareAccessSecret string,
	shareAccessTTL time.Duration,
) *Service {
	if resumableChunkSize <= 0 {
		resumableChunkSize = 8 << 20
	}
	if uploadSessionTTL <= 0 {
		uploadSessionTTL = 24 * time.Hour
	}
	if shareAccessTTL <= 0 {
		shareAccessTTL = 12 * time.Hour
	}
	shareAccessSecret = strings.TrimSpace(shareAccessSecret)
	if shareAccessSecret == "" {
		shareAccessSecret = "sharepier-dev-share-access"
	}

	return &Service{
		db:                 database,
		store:              store,
		publicBaseURL:      strings.TrimRight(publicBaseURL, "/"),
		storageQuotaBytes:  storageQuotaBytes,
		resumableChunkSize: resumableChunkSize,
		uploadSessionTTL:   uploadSessionTTL,
		shareAccessSecret:  shareAccessSecret,
		shareAccessTTL:     shareAccessTTL,
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

	if params.ExpectedSize > 0 {
		if err := s.ensureStorageQuota(ctx, params.ExpectedSize, params.QuotaReservationToken); err != nil {
			return FileRecord{}, err
		}
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

	if err := s.store.PutFile(ctx, tempPath, storageKey, contentType); err != nil {
		return FileRecord{}, err
	}

	record, err := s.insertUpload(ctx, uploadInsertParams{
		OwnerUserID:           params.OwnerUserID,
		StorageKey:            storageKey,
		SHA256:                sha256Hex,
		Size:                  totalSize,
		ContentType:           contentType,
		OriginalName:          originalName,
		DisplayName:           displayName,
		PublicID:              publicID,
		QuotaReservationToken: params.QuotaReservationToken,
	})
	if err != nil {
		_ = s.store.Delete(ctx, storageKey)
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
			f.expires_at,
			f.access_password_hash,
			f.max_downloads,
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
		var expiresAt sql.NullTime
		var accessPasswordHash sql.NullString
		var maxDownloads sql.NullInt64
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
			&expiresAt,
			&accessPasswordHash,
			&maxDownloads,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		item.DownloadURL = s.downloadURL(item.PublicID, item.DisplayName)
		applyShareMetadata(&item, expiresAt, accessPasswordHash, maxDownloads)
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func (s *Service) StorageStats(ctx context.Context) (StorageStats, error) {
	var (
		stats          StorageStats
		latestObjectAt sql.NullTime
	)

	err := s.db.QueryRowContext(
		ctx,
		`select
			coalesce((select count(*) from files), 0),
			coalesce((select count(*) from files where status = 'active'), 0),
			coalesce((select count(*) from files where status = 'disabled'), 0),
			coalesce((select sum(download_count) from files), 0),
			coalesce((select count(*) from objects), 0),
			coalesce((select sum(size) from objects), 0),
			(select max(created_at) from objects)`,
	).Scan(
		&stats.TotalFiles,
		&stats.ActiveFiles,
		&stats.DisabledFiles,
		&stats.TotalDownloads,
		&stats.ObjectCount,
		&stats.StoredBytes,
		&latestObjectAt,
	)
	if err != nil {
		return StorageStats{}, err
	}

	if latestObjectAt.Valid {
		value := latestObjectAt.Time.UTC()
		stats.LatestObjectAt = &value
	}

	return stats, nil
}

func (s *Service) GetByID(ctx context.Context, fileID int64) (FileRecord, error) {
	items, err := s.ListByIDs(ctx, []int64{fileID})
	if err != nil {
		return FileRecord{}, err
	}
	if len(items) == 0 {
		return FileRecord{}, ErrNotFound
	}

	return items[0], nil
}

func (s *Service) ListByIDs(ctx context.Context, fileIDs []int64) ([]FileRecord, error) {
	normalized := normalizeFileIDs(fileIDs)
	if len(normalized) == 0 {
		return nil, nil
	}

	args := make([]any, 0, len(normalized))
	placeholders := make([]string, 0, len(normalized))
	for _, fileID := range normalized {
		args = append(args, fileID)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}

	rows, err := s.db.QueryContext(
		ctx,
		fmt.Sprintf(
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
				f.expires_at,
				f.access_password_hash,
				f.max_downloads,
				f.created_at,
				f.updated_at
			from files f
			join objects o on o.id = f.object_id
			where f.id in (%s)
			order by f.created_at desc`,
			strings.Join(placeholders, ", "),
		),
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]FileRecord, 0, len(normalized))
	for rows.Next() {
		var item FileRecord
		var expiresAt sql.NullTime
		var accessPasswordHash sql.NullString
		var maxDownloads sql.NullInt64
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
			&expiresAt,
			&accessPasswordHash,
			&maxDownloads,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		item.DownloadURL = s.downloadURL(item.PublicID, item.DisplayName)
		applyShareMetadata(&item, expiresAt, accessPasswordHash, maxDownloads)
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
	var expiresAt sql.NullTime
	var accessPasswordHash sql.NullString
	var maxDownloads sql.NullInt64
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
			f.expires_at,
			f.access_password_hash,
			f.max_downloads,
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
		&expiresAt,
		&accessPasswordHash,
		&maxDownloads,
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
	applyShareMetadata(&item, expiresAt, accessPasswordHash, maxDownloads)
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
		_ = s.store.Delete(ctx, storageKey)
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

func (s *Service) GetPublicDownload(ctx context.Context, publicID, accessToken string) (PublicDownload, error) {
	state, err := s.loadPublicAccessState(ctx, publicID)
	if err != nil {
		return PublicDownload{}, err
	}
	if err := s.authorizePublicAccess(state, accessToken); err != nil {
		return PublicDownload{}, err
	}

	return PublicDownload{
		FileID:      state.FileID,
		PublicID:    state.PublicID,
		StorageKey:  state.StorageKey,
		FileName:    state.DisplayName,
		ContentType: state.ContentType,
		Size:        state.Size,
		SHA256:      state.SHA256,
		UpdatedAt:   state.UpdatedAt,
	}, nil
}

func (s *Service) GetPublicFile(ctx context.Context, publicID, accessToken string) (PublicFileRecord, error) {
	state, err := s.loadPublicAccessState(ctx, publicID)
	if err != nil {
		return PublicFileRecord{}, err
	}
	if err := s.authorizePublicAccess(state, accessToken); err != nil {
		return PublicFileRecord{}, err
	}

	item := PublicFileRecord{
		PublicID:          state.PublicID,
		OriginalName:      state.OriginalName,
		DisplayName:       state.DisplayName,
		ContentType:       state.ContentType,
		Size:              state.Size,
		DownloadCount:     state.DownloadCount,
		SHA256:            state.SHA256,
		DownloadURL:       s.downloadURL(state.PublicID, state.DisplayName),
		PasswordProtected: state.AccessPasswordHash.Valid && strings.TrimSpace(state.AccessPasswordHash.String) != "",
		CreatedAt:         state.CreatedAt,
		UpdatedAt:         state.UpdatedAt,
	}
	applyPublicShareMetadata(&item, state.ExpiresAt, state.MaxDownloads)
	return item, nil
}

func (s *Service) BuildShareAccessGrant(ctx context.Context, publicID, password string) (ShareAccessGrant, error) {
	state, err := s.loadPublicAccessState(ctx, publicID)
	if err != nil {
		return ShareAccessGrant{}, err
	}
	if err := s.ensureShareAvailable(state); err != nil {
		return ShareAccessGrant{}, err
	}
	if !state.AccessPasswordHash.Valid || strings.TrimSpace(state.AccessPasswordHash.String) == "" {
		return ShareAccessGrant{}, nil
	}
	if err := bcrypt.CompareHashAndPassword([]byte(state.AccessPasswordHash.String), []byte(strings.TrimSpace(password))); err != nil {
		return ShareAccessGrant{}, ErrSharePasswordInvalid
	}

	expiresAt := time.Now().UTC().Add(s.shareAccessTTL)
	if state.ExpiresAt.Valid && state.ExpiresAt.Time.Before(expiresAt) {
		expiresAt = state.ExpiresAt.Time
	}

	return ShareAccessGrant{
		Token:     s.signPublicAccessToken(state, expiresAt),
		ExpiresAt: expiresAt,
	}, nil
}

func (s *Service) UpdateSharePolicy(ctx context.Context, fileID int64, params SharePolicyUpdateParams) (FileRecord, error) {
	passwordMode := strings.ToLower(strings.TrimSpace(params.PasswordMode))
	if passwordMode == "" {
		passwordMode = "keep"
	}

	switch passwordMode {
	case "keep", "clear", "set":
	default:
		return FileRecord{}, ErrInvalidSharePolicy
	}

	if params.ExpiresAt != nil {
		expiresAt := params.ExpiresAt.UTC()
		if !expiresAt.After(time.Now().UTC()) {
			return FileRecord{}, fmt.Errorf("%w: expires_at must be in the future", ErrInvalidSharePolicy)
		}
		params.ExpiresAt = &expiresAt
	}

	if params.MaxDownloads != nil && *params.MaxDownloads <= 0 {
		return FileRecord{}, fmt.Errorf("%w: max_downloads must be positive", ErrInvalidSharePolicy)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FileRecord{}, err
	}
	defer tx.Rollback()

	var currentHash sql.NullString
	if err := tx.QueryRowContext(ctx, `select access_password_hash from files where id = $1`, fileID).Scan(&currentHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FileRecord{}, ErrNotFound
		}
		return FileRecord{}, err
	}

	nextHash := currentHash
	switch passwordMode {
	case "clear":
		nextHash = sql.NullString{}
	case "set":
		password := strings.TrimSpace(params.Password)
		if password == "" {
			return FileRecord{}, fmt.Errorf("%w: password must not be empty", ErrInvalidSharePolicy)
		}
		passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return FileRecord{}, err
		}
		nextHash = sql.NullString{String: string(passwordHash), Valid: true}
	}

	var expiresValue any
	if params.ExpiresAt != nil {
		expiresValue = params.ExpiresAt.UTC()
	}

	var maxDownloadsValue any
	if params.MaxDownloads != nil {
		maxDownloadsValue = *params.MaxDownloads
	}

	var item FileRecord
	var expiresAt sql.NullTime
	var accessPasswordHash sql.NullString
	var maxDownloads sql.NullInt64
	err = tx.QueryRowContext(
		ctx,
		`update files as f
		set expires_at = $2,
			max_downloads = $3,
			access_password_hash = $4,
			updated_at = now()
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
			f.expires_at,
			f.access_password_hash,
			f.max_downloads,
			f.created_at,
			f.updated_at`,
		fileID,
		expiresValue,
		maxDownloadsValue,
		nullStringValue(nextHash),
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
		&expiresAt,
		&accessPasswordHash,
		&maxDownloads,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FileRecord{}, ErrNotFound
		}
		return FileRecord{}, err
	}

	if err := tx.Commit(); err != nil {
		return FileRecord{}, err
	}

	item.DownloadURL = s.downloadURL(item.PublicID, item.DisplayName)
	applyShareMetadata(&item, expiresAt, accessPasswordHash, maxDownloads)
	return item, nil
}

func (s *Service) RecordDownload(ctx context.Context, fileID int64, ipAddress, userAgent, referer string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(
		ctx,
		`update files
		set download_count = download_count + 1
		where id = $1 and (max_downloads is null or download_count < max_downloads)`,
		fileID,
	)
	if err != nil {
		return err
	}
	affectedRows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affectedRows == 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `select exists(select 1 from files where id = $1)`, fileID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}

		return ErrShareDownloadLimitReached
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

func (s *Service) loadPublicAccessState(ctx context.Context, publicID string) (publicAccessState, error) {
	publicID = strings.TrimSpace(publicID)
	if publicID == "" {
		return publicAccessState{}, ErrNotFound
	}

	var state publicAccessState
	err := s.db.QueryRowContext(
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
			o.sha256,
			o.storage_key,
			f.expires_at,
			f.max_downloads,
			f.access_password_hash,
			f.created_at,
			f.updated_at
		from files as f
		join objects as o on o.id = f.object_id
		where f.public_id = $1 and f.status = 'active' and f.visibility = 'public'`,
		publicID,
	).Scan(
		&state.FileID,
		&state.PublicID,
		&state.OriginalName,
		&state.DisplayName,
		&state.Status,
		&state.Visibility,
		&state.ContentType,
		&state.Size,
		&state.DownloadCount,
		&state.SHA256,
		&state.StorageKey,
		&state.ExpiresAt,
		&state.MaxDownloads,
		&state.AccessPasswordHash,
		&state.CreatedAt,
		&state.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return publicAccessState{}, ErrNotFound
		}
		return publicAccessState{}, err
	}

	return state, nil
}

func (s *Service) authorizePublicAccess(state publicAccessState, accessToken string) error {
	if err := s.ensureShareAvailable(state); err != nil {
		return err
	}

	if state.AccessPasswordHash.Valid && strings.TrimSpace(state.AccessPasswordHash.String) != "" {
		if !s.verifyPublicAccessToken(state, accessToken) {
			return ErrSharePasswordRequired
		}
	}

	return nil
}

func (s *Service) ensureShareAvailable(state publicAccessState) error {
	now := time.Now().UTC()
	if state.ExpiresAt.Valid && now.After(state.ExpiresAt.Time) {
		return ErrShareExpired
	}
	if state.MaxDownloads.Valid && state.DownloadCount >= state.MaxDownloads.Int64 {
		return ErrShareDownloadLimitReached
	}

	return nil
}

func (s *Service) signPublicAccessToken(state publicAccessState, expiresAt time.Time) string {
	payload := strings.Join([]string{
		state.PublicID,
		strconv.FormatInt(expiresAt.UTC().Unix(), 10),
		strconv.FormatInt(state.UpdatedAt.UTC().UnixNano(), 10),
		strings.TrimSpace(state.AccessPasswordHash.String),
	}, ":")
	mac := hmac.New(sha256.New, []byte(s.shareAccessSecret))
	_, _ = mac.Write([]byte(payload))
	signature := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("%d.%s", expiresAt.UTC().Unix(), signature)
}

func (s *Service) verifyPublicAccessToken(state publicAccessState, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}

	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return false
	}

	expiresUnix, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || expiresUnix <= 0 {
		return false
	}
	expiresAt := time.Unix(expiresUnix, 0).UTC()
	if time.Now().UTC().After(expiresAt) {
		return false
	}

	expected := s.signPublicAccessToken(state, expiresAt)
	return hmac.Equal([]byte(expected), []byte(token))
}

func applyShareMetadata(item *FileRecord, expiresAt sql.NullTime, accessPasswordHash sql.NullString, maxDownloads sql.NullInt64) {
	if expiresAt.Valid {
		value := expiresAt.Time.UTC()
		item.ExpiresAt = &value
	}
	if maxDownloads.Valid {
		value := maxDownloads.Int64
		item.MaxDownloads = &value
		remaining := maxDownloads.Int64 - item.DownloadCount
		if remaining < 0 {
			remaining = 0
		}
		item.RemainingDownloads = &remaining
	}
	item.PasswordProtected = accessPasswordHash.Valid && strings.TrimSpace(accessPasswordHash.String) != ""
}

func applyPublicShareMetadata(item *PublicFileRecord, expiresAt sql.NullTime, maxDownloads sql.NullInt64) {
	if expiresAt.Valid {
		value := expiresAt.Time.UTC()
		item.ExpiresAt = &value
	}
	if maxDownloads.Valid {
		value := maxDownloads.Int64
		item.MaxDownloads = &value
		remaining := maxDownloads.Int64 - item.DownloadCount
		if remaining < 0 {
			remaining = 0
		}
		item.RemainingDownloads = &remaining
	}
}

func nullStringValue(value sql.NullString) any {
	if !value.Valid {
		return nil
	}

	return value.String
}

type uploadInsertParams struct {
	OwnerUserID           int64
	StorageKey            string
	SHA256                string
	Size                  int64
	ContentType           string
	OriginalName          string
	DisplayName           string
	PublicID              string
	QuotaReservationToken string
}

func (s *Service) insertUpload(ctx context.Context, params uploadInsertParams) (FileRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FileRecord{}, err
	}
	defer tx.Rollback()

	if err := s.lockStorageQuotaTx(ctx, tx); err != nil {
		return FileRecord{}, err
	}
	if err := s.ensureStorageQuotaTx(ctx, tx, params.Size, params.QuotaReservationToken); err != nil {
		return FileRecord{}, err
	}

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

func (s *Service) ensureStorageQuota(ctx context.Context, incomingBytes int64, excludeUploadToken string) error {
	if s.storageQuotaBytes <= 0 || incomingBytes <= 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := s.ensureStorageQuotaTx(ctx, tx, incomingBytes, excludeUploadToken); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Service) ensureStorageQuotaTx(ctx context.Context, tx *sql.Tx, incomingBytes int64, excludeUploadToken string) error {
	if s.storageQuotaBytes <= 0 || incomingBytes <= 0 {
		return nil
	}

	var (
		storedBytes   int64
		reservedBytes int64
	)
	err := tx.QueryRowContext(
		ctx,
		`select
			coalesce((select sum(size) from objects), 0),
			coalesce((
				select sum(total_size)
				from upload_sessions
				where expired_at > now()
				  and status in ('pending', 'uploading', 'uploaded', 'completing')
				  and ($1 = '' or upload_token <> $1)
			), 0)`,
		strings.TrimSpace(excludeUploadToken),
	).Scan(&storedBytes, &reservedBytes)
	if err != nil {
		return err
	}

	if storedBytes+reservedBytes+incomingBytes <= s.storageQuotaBytes {
		return nil
	}

	return ErrStorageQuotaExceeded
}

func (s *Service) lockStorageQuotaTx(ctx context.Context, tx *sql.Tx) error {
	if s.storageQuotaBytes <= 0 {
		return nil
	}

	_, err := tx.ExecContext(ctx, `select pg_advisory_xact_lock($1)`, int64(2026040301))
	return err
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
