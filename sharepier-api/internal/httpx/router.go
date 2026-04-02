package httpx

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	cors "github.com/go-chi/cors"

	"sharepier-api/internal/audit"
	"sharepier-api/internal/auth"
	"sharepier-api/internal/config"
	"sharepier-api/internal/files"
	"sharepier-api/internal/storage"
)

type statusResponse struct {
	Status          string `json:"status"`
	Name            string `json:"name,omitempty"`
	Version         string `json:"version,omitempty"`
	Environment     string `json:"environment,omitempty"`
	Timestamp       string `json:"timestamp,omitempty"`
	Message         string `json:"message,omitempty"`
	StorageBackend  string `json:"storageBackend,omitempty"`
	StorageLocation string `json:"storageLocation,omitempty"`
	StorageRoot     string `json:"storageRoot,omitempty"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authResponse struct {
	Status    string     `json:"status"`
	User      *auth.User `json:"user,omitempty"`
	Message   string     `json:"message,omitempty"`
	Timestamp string     `json:"timestamp,omitempty"`
}

type filesResponse struct {
	Status        string             `json:"status"`
	Items         []files.FileRecord `json:"items,omitempty"`
	Item          *files.FileRecord  `json:"item,omitempty"`
	AffectedCount int64              `json:"affectedCount,omitempty"`
	Message       string             `json:"message,omitempty"`
	Timestamp     string             `json:"timestamp,omitempty"`
}

type publicFileResponse struct {
	Status           string                  `json:"status"`
	Item             *files.PublicFileRecord `json:"item,omitempty"`
	PasswordRequired bool                    `json:"passwordRequired,omitempty"`
	Message          string                  `json:"message,omitempty"`
	Timestamp        string                  `json:"timestamp,omitempty"`
}

type uploadSessionResponse struct {
	Status    string               `json:"status"`
	Session   *files.UploadSession `json:"session,omitempty"`
	Item      *files.FileRecord    `json:"item,omitempty"`
	Message   string               `json:"message,omitempty"`
	Timestamp string               `json:"timestamp,omitempty"`
}

type auditLogsResponse struct {
	Status    string         `json:"status"`
	Items     []audit.Record `json:"items,omitempty"`
	Message   string         `json:"message,omitempty"`
	Timestamp string         `json:"timestamp,omitempty"`
}

type storageStatsResponse struct {
	Status          string              `json:"status"`
	Stats           *files.StorageStats `json:"stats,omitempty"`
	StorageBackend  string              `json:"storageBackend,omitempty"`
	StorageLocation string              `json:"storageLocation,omitempty"`
	StorageRoot     string              `json:"storageRoot,omitempty"`
	QuotaBytes      int64               `json:"quotaBytes,omitempty"`
	RemainingBytes  *int64              `json:"remainingBytes,omitempty"`
	UsagePercent    *float64            `json:"usagePercent,omitempty"`
	Message         string              `json:"message,omitempty"`
	Timestamp       string              `json:"timestamp,omitempty"`
}

func NewRouter(
	cfg config.Config,
	logger *slog.Logger,
	store storage.Store,
	authService *auth.Service,
	fileService *files.Service,
	auditService *audit.Service,
) http.Handler {
	router := chi.NewRouter()

	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(60 * time.Second))
	router.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	router.Get("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, statusResponse{
			Status:          "ok",
			Name:            cfg.AppName,
			Version:         "0.1.0-skeleton",
			Environment:     cfg.Env,
			Timestamp:       time.Now().UTC().Format(time.RFC3339),
			StorageBackend:  store.Backend(),
			StorageLocation: store.Location(),
			StorageRoot:     store.Root(),
		})
	})

	logAuditEvent := func(ctx context.Context, event audit.Event) {
		if auditService == nil {
			return
		}
		if err := auditService.Log(ctx, event); err != nil {
			logger.Warn("failed to persist audit log", slog.String("error", err.Error()), slog.String("action", event.Action))
		}
	}

	router.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/login", func(w http.ResponseWriter, r *http.Request) {
			var payload loginRequest
			if err := readJSON(r, &payload); err != nil {
				writeJSON(w, http.StatusBadRequest, authResponse{
					Status:    "bad_request",
					Message:   "invalid request payload",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			user, token, expiresAt, err := authService.Login(r.Context(), payload.Username, payload.Password)
			if err != nil {
				switch {
				case errors.Is(err, auth.ErrInvalidCredentials):
					logAuditEvent(r.Context(), audit.Event{
						ActorUsername: strings.TrimSpace(payload.Username),
						Action:        "auth_login_failed",
						IPAddress:     r.RemoteAddr,
						UserAgent:     r.UserAgent(),
					})
					writeJSON(w, http.StatusUnauthorized, authResponse{
						Status:    "unauthorized",
						Message:   "用户名或密码错误",
						Timestamp: time.Now().UTC().Format(time.RFC3339),
					})
				default:
					writeJSON(w, http.StatusInternalServerError, authResponse{
						Status:    "error",
						Message:   "login failed",
						Timestamp: time.Now().UTC().Format(time.RFC3339),
					})
				}
				return
			}

			authService.SetSessionCookie(w, token, expiresAt)
			logAuditEvent(r.Context(), audit.Event{
				ActorUserID:   &user.ID,
				ActorUsername: user.Username,
				Action:        "auth_login",
				Metadata: map[string]any{
					"role": user.Role,
				},
				IPAddress: r.RemoteAddr,
				UserAgent: r.UserAgent(),
			})
			writeJSON(w, http.StatusOK, authResponse{
				Status:    "ok",
				User:      &user,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		})

		r.Post("/auth/logout", func(w http.ResponseWriter, r *http.Request) {
			token := authService.SessionTokenFromRequest(r)
			currentUser, _ := authService.CurrentUser(r.Context(), token)
			if err := authService.Logout(r.Context(), token); err != nil {
				writeJSON(w, http.StatusInternalServerError, authResponse{
					Status:    "error",
					Message:   "logout failed",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			authService.ClearSessionCookie(w)
			if currentUser.ID > 0 {
				logAuditEvent(r.Context(), audit.Event{
					ActorUserID:   &currentUser.ID,
					ActorUsername: currentUser.Username,
					Action:        "auth_logout",
					IPAddress:     r.RemoteAddr,
					UserAgent:     r.UserAgent(),
				})
			}
			writeJSON(w, http.StatusOK, authResponse{
				Status:    "ok",
				Message:   "logged out",
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		})

		r.Get("/auth/me", func(w http.ResponseWriter, r *http.Request) {
			user, err := authService.CurrentUser(r.Context(), authService.SessionTokenFromRequest(r))
			if err != nil {
				status := http.StatusInternalServerError
				message := "failed to load current user"
				if errors.Is(err, auth.ErrUnauthorized) {
					status = http.StatusUnauthorized
					message = "authentication required"
				}

				writeJSON(w, status, authResponse{
					Status:    strings.ReplaceAll(http.StatusText(status), " ", "_"),
					Message:   strings.ToLower(message),
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			writeJSON(w, http.StatusOK, authResponse{
				Status:    "ok",
				User:      &user,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		})

		r.With(requireAuth(authService)).Get("/audit/logs", func(w http.ResponseWriter, r *http.Request) {
			format := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("format")))
			limit := 50
			if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
				if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
					limit = parsed
				}
			}

			action := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("action")))
			if !audit.IsValidAction(action) {
				writeJSON(w, http.StatusBadRequest, auditLogsResponse{
					Status:    "bad_request",
					Message:   "unsupported audit action",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			queryText := strings.TrimSpace(r.URL.Query().Get("query"))
			var (
				from *time.Time
				to   *time.Time
			)
			if raw := strings.TrimSpace(r.URL.Query().Get("from")); raw != "" {
				parsed, err := time.Parse(time.RFC3339, raw)
				if err != nil {
					writeJSON(w, http.StatusBadRequest, auditLogsResponse{
						Status:    "bad_request",
						Message:   "invalid from value",
						Timestamp: time.Now().UTC().Format(time.RFC3339),
					})
					return
				}
				from = &parsed
			}
			if raw := strings.TrimSpace(r.URL.Query().Get("to")); raw != "" {
				parsed, err := time.Parse(time.RFC3339, raw)
				if err != nil {
					writeJSON(w, http.StatusBadRequest, auditLogsResponse{
						Status:    "bad_request",
						Message:   "invalid to value",
						Timestamp: time.Now().UTC().Format(time.RFC3339),
					})
					return
				}
				to = &parsed
			}

			items, err := auditService.List(r.Context(), audit.ListParams{
				Limit:  limit,
				Action: action,
				Query:  queryText,
				From:   from,
				To:     to,
			})
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, auditLogsResponse{
					Status:    "error",
					Message:   "failed to load audit logs",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			if format == "csv" {
				if err := writeAuditLogsCSV(w, items); err != nil {
					logger.Warn("failed to export audit logs csv", slog.String("error", err.Error()))
					writeJSON(w, http.StatusInternalServerError, auditLogsResponse{
						Status:    "error",
						Message:   "failed to export audit logs",
						Timestamp: time.Now().UTC().Format(time.RFC3339),
					})
				}
				return
			}

			writeJSON(w, http.StatusOK, auditLogsResponse{
				Status:    "ok",
				Items:     items,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		})

		r.With(requireAuth(authService)).Get("/stats/storage", func(w http.ResponseWriter, r *http.Request) {
			stats, err := fileService.StorageStats(r.Context())
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, storageStatsResponse{
					Status:    "error",
					Message:   "failed to load storage stats",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			response := storageStatsResponse{
				Status:          "ok",
				Stats:           &stats,
				StorageBackend:  store.Backend(),
				StorageLocation: store.Location(),
				StorageRoot:     store.Root(),
				QuotaBytes:      cfg.StorageQuotaBytes,
				Timestamp:       time.Now().UTC().Format(time.RFC3339),
			}

			if cfg.StorageQuotaBytes > 0 {
				remaining := cfg.StorageQuotaBytes - stats.StoredBytes
				if remaining < 0 {
					remaining = 0
				}
				usagePercent := float64(stats.StoredBytes) / float64(cfg.StorageQuotaBytes) * 100
				if usagePercent < 0 {
					usagePercent = 0
				}
				if usagePercent > 100 {
					usagePercent = 100
				}

				response.RemainingBytes = &remaining
				response.UsagePercent = &usagePercent
			}

			writeJSON(w, http.StatusOK, response)
		})

		r.With(requireAuth(authService)).Get("/files", func(w http.ResponseWriter, r *http.Request) {
			items, err := fileService.List(r.Context())
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, filesResponse{
					Status:    "error",
					Message:   "failed to load files",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			writeJSON(w, http.StatusOK, filesResponse{
				Status:    "ok",
				Items:     items,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		})

		r.With(requireAuth(authService)).Patch("/files/{fileID}", func(w http.ResponseWriter, r *http.Request) {
			fileID, err := parseInt64Param(r, "fileID")
			if err != nil {
				writeJSON(w, http.StatusBadRequest, filesResponse{
					Status:    "bad_request",
					Message:   "invalid file id",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			var payload struct {
				Status string `json:"status"`
			}
			if err := readJSON(r, &payload); err != nil {
				writeJSON(w, http.StatusBadRequest, filesResponse{
					Status:    "bad_request",
					Message:   "invalid request payload",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			before, _ := fileService.GetByID(r.Context(), fileID)
			item, err := fileService.SetStatus(r.Context(), fileID, payload.Status)
			if err != nil {
				statusCode := http.StatusInternalServerError
				message := "failed to update file"

				switch {
				case errors.Is(err, files.ErrNotFound):
					statusCode = http.StatusNotFound
					message = "file not found"
				case errors.Is(err, files.ErrInvalidStatus):
					statusCode = http.StatusBadRequest
					message = "unsupported file status"
				}

				writeJSON(w, statusCode, filesResponse{
					Status:    strings.ToLower(strings.ReplaceAll(http.StatusText(statusCode), " ", "_")),
					Message:   message,
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			if user, ok := auth.UserFromContext(r.Context()); ok {
				logAuditEvent(r.Context(), audit.Event{
					ActorUserID:     &user.ID,
					ActorUsername:   user.Username,
					Action:          "file_status_change",
					FileID:          &item.ID,
					FilePublicID:    item.PublicID,
					FileDisplayName: item.DisplayName,
					Metadata: map[string]any{
						"from": before.Status,
						"to":   item.Status,
					},
					IPAddress: r.RemoteAddr,
					UserAgent: r.UserAgent(),
				})
			}

			writeJSON(w, http.StatusOK, filesResponse{
				Status:    "ok",
				Item:      &item,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		})

		r.With(requireAuth(authService)).Patch("/files/{fileID}/share-policy", func(w http.ResponseWriter, r *http.Request) {
			fileID, err := parseInt64Param(r, "fileID")
			if err != nil {
				writeJSON(w, http.StatusBadRequest, filesResponse{
					Status:    "bad_request",
					Message:   "invalid file id",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			var payload struct {
				ExpiresAt    *string `json:"expiresAt"`
				MaxDownloads *int64  `json:"maxDownloads"`
				PasswordMode string  `json:"passwordMode"`
				Password     string  `json:"password"`
			}
			if err := readJSON(r, &payload); err != nil {
				writeJSON(w, http.StatusBadRequest, filesResponse{
					Status:    "bad_request",
					Message:   "invalid request payload",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			var expiresAt *time.Time
			if payload.ExpiresAt != nil && strings.TrimSpace(*payload.ExpiresAt) != "" {
				parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*payload.ExpiresAt))
				if err != nil {
					writeJSON(w, http.StatusBadRequest, filesResponse{
						Status:    "bad_request",
						Message:   "invalid expiresAt value",
						Timestamp: time.Now().UTC().Format(time.RFC3339),
					})
					return
				}

				expiresAt = &parsed
			}

			item, err := fileService.UpdateSharePolicy(r.Context(), fileID, files.SharePolicyUpdateParams{
				ExpiresAt:    expiresAt,
				MaxDownloads: payload.MaxDownloads,
				PasswordMode: payload.PasswordMode,
				Password:     payload.Password,
			})
			if err != nil {
				statusCode := http.StatusInternalServerError
				message := "failed to update share policy"

				switch {
				case errors.Is(err, files.ErrNotFound):
					statusCode = http.StatusNotFound
					message = "file not found"
				case errors.Is(err, files.ErrInvalidSharePolicy):
					statusCode = http.StatusBadRequest
					message = err.Error()
				}

				writeJSON(w, statusCode, filesResponse{
					Status:    strings.ToLower(strings.ReplaceAll(http.StatusText(statusCode), " ", "_")),
					Message:   message,
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			if user, ok := auth.UserFromContext(r.Context()); ok {
				logAuditEvent(r.Context(), audit.Event{
					ActorUserID:     &user.ID,
					ActorUsername:   user.Username,
					Action:          "file_share_policy_update",
					FileID:          &item.ID,
					FilePublicID:    item.PublicID,
					FileDisplayName: item.DisplayName,
					Metadata: map[string]any{
						"expiresAt":         item.ExpiresAt,
						"maxDownloads":      item.MaxDownloads,
						"passwordProtected": item.PasswordProtected,
					},
					IPAddress: r.RemoteAddr,
					UserAgent: r.UserAgent(),
				})
			}

			writeJSON(w, http.StatusOK, filesResponse{
				Status:    "ok",
				Item:      &item,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		})

		r.With(requireAuth(authService)).Post("/files/batch", func(w http.ResponseWriter, r *http.Request) {
			var payload struct {
				Action  string  `json:"action"`
				FileIDs []int64 `json:"fileIds"`
				Status  string  `json:"status"`
			}
			if err := readJSON(r, &payload); err != nil {
				writeJSON(w, http.StatusBadRequest, filesResponse{
					Status:    "bad_request",
					Message:   "invalid request payload",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			action := strings.TrimSpace(strings.ToLower(payload.Action))
			selectedItems, _ := fileService.ListByIDs(r.Context(), payload.FileIDs)
			var (
				affectedCount int64
				err           error
				message       string
			)

			switch action {
			case "set_status":
				affectedCount, err = fileService.SetStatusMany(r.Context(), payload.FileIDs, payload.Status)
				if err == nil {
					if strings.EqualFold(payload.Status, "disabled") {
						message = "files disabled"
					} else {
						message = "files enabled"
					}
				}
			case "delete":
				affectedCount, err = fileService.DeleteMany(r.Context(), payload.FileIDs)
				if err == nil {
					message = "files deleted"
				}
			default:
				writeJSON(w, http.StatusBadRequest, filesResponse{
					Status:    "bad_request",
					Message:   "unsupported batch action",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			if err != nil {
				statusCode := http.StatusInternalServerError
				messageText := "failed to process batch action"

				switch {
				case errors.Is(err, files.ErrNotFound):
					statusCode = http.StatusNotFound
					messageText = "files not found"
				case errors.Is(err, files.ErrInvalidStatus):
					statusCode = http.StatusBadRequest
					messageText = "unsupported file status"
				case errors.Is(err, files.ErrInvalidSelection):
					statusCode = http.StatusBadRequest
					messageText = "empty file selection"
				}

				writeJSON(w, statusCode, filesResponse{
					Status:    strings.ToLower(strings.ReplaceAll(http.StatusText(statusCode), " ", "_")),
					Message:   messageText,
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			if user, ok := auth.UserFromContext(r.Context()); ok {
				metadata := map[string]any{
					"affectedCount": affectedCount,
					"fileIds":       payload.FileIDs,
					"fileNames":     collectFileNames(selectedItems),
				}
				actionName := "file_batch_delete"
				if action == "set_status" {
					actionName = "file_batch_set_status"
					metadata["status"] = payload.Status
				}

				logAuditEvent(r.Context(), audit.Event{
					ActorUserID:   &user.ID,
					ActorUsername: user.Username,
					Action:        actionName,
					Metadata:      metadata,
					IPAddress:     r.RemoteAddr,
					UserAgent:     r.UserAgent(),
				})
			}

			writeJSON(w, http.StatusOK, filesResponse{
				Status:        "ok",
				AffectedCount: affectedCount,
				Message:       message,
				Timestamp:     time.Now().UTC().Format(time.RFC3339),
			})
		})

		r.With(requireAuth(authService)).Delete("/files/{fileID}", func(w http.ResponseWriter, r *http.Request) {
			fileID, err := parseInt64Param(r, "fileID")
			if err != nil {
				writeJSON(w, http.StatusBadRequest, filesResponse{
					Status:    "bad_request",
					Message:   "invalid file id",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			item, _ := fileService.GetByID(r.Context(), fileID)
			if err := fileService.Delete(r.Context(), fileID); err != nil {
				statusCode := http.StatusInternalServerError
				message := "failed to delete file"
				if errors.Is(err, files.ErrNotFound) {
					statusCode = http.StatusNotFound
					message = "file not found"
				}

				writeJSON(w, statusCode, filesResponse{
					Status:    strings.ToLower(strings.ReplaceAll(http.StatusText(statusCode), " ", "_")),
					Message:   message,
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			if item.ID > 0 {
				if user, ok := auth.UserFromContext(r.Context()); ok {
					logAuditEvent(r.Context(), audit.Event{
						ActorUserID:     &user.ID,
						ActorUsername:   user.Username,
						Action:          "file_delete",
						FileID:          &item.ID,
						FilePublicID:    item.PublicID,
						FileDisplayName: item.DisplayName,
						Metadata: map[string]any{
							"size": item.Size,
						},
						IPAddress: r.RemoteAddr,
						UserAgent: r.UserAgent(),
					})
				}
			}

			writeJSON(w, http.StatusOK, filesResponse{
				Status:    "ok",
				Message:   "file deleted",
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		})

		r.With(requireAuth(authService)).Post("/uploads", func(w http.ResponseWriter, r *http.Request) {
			user, ok := auth.UserFromContext(r.Context())
			if !ok {
				writeJSON(w, http.StatusUnauthorized, authResponse{
					Status:    "unauthorized",
					Message:   "authentication required",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxUploadSize)
			if err := r.ParseMultipartForm(8 << 20); err != nil {
				writeJSON(w, http.StatusBadRequest, filesResponse{
					Status:    "bad_request",
					Message:   "invalid upload payload or file too large",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}
			defer func() {
				if r.MultipartForm != nil {
					_ = r.MultipartForm.RemoveAll()
				}
			}()

			uploadedFile, header, err := r.FormFile("file")
			if err != nil {
				writeJSON(w, http.StatusBadRequest, filesResponse{
					Status:    "bad_request",
					Message:   "missing file field",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}
			defer uploadedFile.Close()

			record, err := fileService.Upload(r.Context(), files.UploadParams{
				OwnerUserID:  user.ID,
				OriginalName: header.Filename,
				DisplayName:  r.FormValue("displayName"),
				ExpectedSize: header.Size,
				Reader:       uploadedFile,
			})
			if err != nil {
				status := http.StatusInternalServerError
				message := "upload failed"
				if errors.Is(err, files.ErrInvalidUpload) {
					status = http.StatusBadRequest
					message = err.Error()
				} else if errors.Is(err, files.ErrStorageQuotaExceeded) {
					status = http.StatusInsufficientStorage
					message = "storage quota exceeded"
				}

				writeJSON(w, status, filesResponse{
					Status:    strings.ToLower(strings.ReplaceAll(http.StatusText(status), " ", "_")),
					Message:   message,
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			logAuditEventForUser(r, logAuditEvent, audit.Event{
				Action:          "file_upload",
				FileID:          &record.ID,
				FilePublicID:    record.PublicID,
				FileDisplayName: record.DisplayName,
				Metadata: map[string]any{
					"mode": "multipart",
					"size": record.Size,
				},
				IPAddress: r.RemoteAddr,
				UserAgent: r.UserAgent(),
			})

			writeJSON(w, http.StatusCreated, filesResponse{
				Status:    "ok",
				Item:      &record,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		})

		r.With(requireAuth(authService)).Post("/uploads/resumable/sessions", func(w http.ResponseWriter, r *http.Request) {
			user, ok := auth.UserFromContext(r.Context())
			if !ok {
				writeJSON(w, http.StatusUnauthorized, authResponse{
					Status:    "unauthorized",
					Message:   "authentication required",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			var payload struct {
				OriginalName string `json:"originalName"`
				DisplayName  string `json:"displayName"`
				ContentType  string `json:"contentType"`
				TotalSize    int64  `json:"totalSize"`
			}
			if err := readJSON(r, &payload); err != nil {
				writeJSON(w, http.StatusBadRequest, uploadSessionResponse{
					Status:    "bad_request",
					Message:   "invalid request payload",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			session, err := fileService.CreateUploadSession(r.Context(), files.CreateUploadSessionParams{
				OwnerUserID:  user.ID,
				OriginalName: payload.OriginalName,
				DisplayName:  payload.DisplayName,
				ContentType:  payload.ContentType,
				TotalSize:    payload.TotalSize,
			})
			if err != nil {
				statusCode := http.StatusInternalServerError
				message := "failed to create upload session"
				if errors.Is(err, files.ErrInvalidUpload) {
					statusCode = http.StatusBadRequest
					message = err.Error()
				} else if errors.Is(err, files.ErrStorageQuotaExceeded) {
					statusCode = http.StatusInsufficientStorage
					message = "storage quota exceeded"
				}

				writeJSON(w, statusCode, uploadSessionResponse{
					Status:    strings.ToLower(strings.ReplaceAll(http.StatusText(statusCode), " ", "_")),
					Message:   message,
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			writeJSON(w, http.StatusCreated, uploadSessionResponse{
				Status:    "ok",
				Session:   &session,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		})

		r.With(requireAuth(authService)).Get("/uploads/resumable/{uploadToken}", func(w http.ResponseWriter, r *http.Request) {
			session, err := fileService.GetUploadSession(r.Context(), chi.URLParam(r, "uploadToken"))
			if err != nil {
				statusCode := http.StatusInternalServerError
				message := "failed to load upload session"

				switch {
				case errors.Is(err, files.ErrNotFound):
					statusCode = http.StatusNotFound
					message = "upload session not found"
				case errors.Is(err, files.ErrUploadSessionExpired):
					statusCode = http.StatusGone
					message = "upload session expired"
				}

				writeJSON(w, statusCode, uploadSessionResponse{
					Status:    strings.ToLower(strings.ReplaceAll(http.StatusText(statusCode), " ", "_")),
					Message:   message,
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			writeJSON(w, http.StatusOK, uploadSessionResponse{
				Status:    "ok",
				Session:   &session,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		})

		r.With(requireAuth(authService)).Put("/uploads/resumable/{uploadToken}", func(w http.ResponseWriter, r *http.Request) {
			offsetHeader := strings.TrimSpace(r.Header.Get("X-Upload-Offset"))
			if offsetHeader == "" {
				writeJSON(w, http.StatusBadRequest, uploadSessionResponse{
					Status:    "bad_request",
					Message:   "missing x-upload-offset header",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			offset, err := strconv.ParseInt(offsetHeader, 10, 64)
			if err != nil || offset < 0 {
				writeJSON(w, http.StatusBadRequest, uploadSessionResponse{
					Status:    "bad_request",
					Message:   "invalid upload offset",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			r.Body = http.MaxBytesReader(w, r.Body, cfg.ResumableChunkSize+1)
			session, err := fileService.AppendUploadChunk(r.Context(), chi.URLParam(r, "uploadToken"), offset, r.Body)
			if err != nil {
				statusCode := http.StatusInternalServerError
				message := "failed to append upload chunk"

				switch {
				case errors.Is(err, files.ErrNotFound):
					statusCode = http.StatusNotFound
					message = "upload session not found"
				case errors.Is(err, files.ErrUploadSessionExpired):
					statusCode = http.StatusGone
					message = "upload session expired"
				case errors.Is(err, files.ErrUploadOffsetMismatch):
					statusCode = http.StatusConflict
					message = "upload offset mismatch"
				case errors.Is(err, files.ErrUploadChunkTooLarge), errors.Is(err, files.ErrInvalidUpload):
					statusCode = http.StatusBadRequest
					message = err.Error()
				case errors.Is(err, files.ErrUploadSessionNotReady):
					statusCode = http.StatusConflict
					message = "upload session is not ready for more chunks"
				}

				writeJSON(w, statusCode, uploadSessionResponse{
					Status:    strings.ToLower(strings.ReplaceAll(http.StatusText(statusCode), " ", "_")),
					Message:   message,
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			writeJSON(w, http.StatusOK, uploadSessionResponse{
				Status:    "ok",
				Session:   &session,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		})

		r.With(requireAuth(authService)).Post("/uploads/resumable/{uploadToken}/complete", func(w http.ResponseWriter, r *http.Request) {
			record, err := fileService.CompleteUploadSession(r.Context(), chi.URLParam(r, "uploadToken"))
			if err != nil {
				statusCode := http.StatusInternalServerError
				message := "failed to complete upload session"

				switch {
				case errors.Is(err, files.ErrNotFound):
					statusCode = http.StatusNotFound
					message = "upload session not found"
				case errors.Is(err, files.ErrUploadSessionExpired):
					statusCode = http.StatusGone
					message = "upload session expired"
				case errors.Is(err, files.ErrUploadSessionNotReady):
					statusCode = http.StatusConflict
					message = "upload session is not fully uploaded"
				case errors.Is(err, files.ErrStorageQuotaExceeded):
					statusCode = http.StatusInsufficientStorage
					message = "storage quota exceeded"
				}

				writeJSON(w, statusCode, uploadSessionResponse{
					Status:    strings.ToLower(strings.ReplaceAll(http.StatusText(statusCode), " ", "_")),
					Message:   message,
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			logAuditEventForUser(r, logAuditEvent, audit.Event{
				Action:          "file_upload",
				FileID:          &record.ID,
				FilePublicID:    record.PublicID,
				FileDisplayName: record.DisplayName,
				Metadata: map[string]any{
					"mode": "resumable",
					"size": record.Size,
				},
				IPAddress: r.RemoteAddr,
				UserAgent: r.UserAgent(),
			})

			writeJSON(w, http.StatusCreated, uploadSessionResponse{
				Status:    "ok",
				Item:      &record,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		})

		r.Post("/public/files/{publicID}/unlock", func(w http.ResponseWriter, r *http.Request) {
			publicID := chi.URLParam(r, "publicID")

			var payload struct {
				Password string `json:"password"`
			}
			if err := readJSON(r, &payload); err != nil {
				writeJSON(w, http.StatusBadRequest, publicFileResponse{
					Status:    "bad_request",
					Message:   "invalid request payload",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			grant, err := fileService.BuildShareAccessGrant(r.Context(), publicID, payload.Password)
			if err != nil {
				statusCode := http.StatusInternalServerError
				message := "failed to unlock share"
				passwordRequired := false

				switch {
				case errors.Is(err, files.ErrNotFound):
					statusCode = http.StatusNotFound
					message = "file not found"
				case errors.Is(err, files.ErrSharePasswordInvalid), errors.Is(err, files.ErrSharePasswordRequired):
					statusCode = http.StatusUnauthorized
					message = "invalid share password"
					passwordRequired = true
				case errors.Is(err, files.ErrShareExpired):
					statusCode = http.StatusGone
					message = "share link expired"
				case errors.Is(err, files.ErrShareDownloadLimitReached):
					statusCode = http.StatusGone
					message = "download limit reached"
				}

				writeJSON(w, statusCode, publicFileResponse{
					Status:           strings.ToLower(strings.ReplaceAll(http.StatusText(statusCode), " ", "_")),
					PasswordRequired: passwordRequired,
					Message:          message,
					Timestamp:        time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			if grant.Token != "" {
				setPublicAccessCookie(w, cfg, publicID, grant.Token, grant.ExpiresAt)
			}

			item, err := fileService.GetPublicFile(r.Context(), publicID, grant.Token)
			if err != nil {
				statusCode := http.StatusInternalServerError
				message := "failed to load public file"

				switch {
				case errors.Is(err, files.ErrNotFound):
					statusCode = http.StatusNotFound
					message = "file not found"
				case errors.Is(err, files.ErrShareExpired):
					statusCode = http.StatusGone
					message = "share link expired"
				case errors.Is(err, files.ErrShareDownloadLimitReached):
					statusCode = http.StatusGone
					message = "download limit reached"
				case errors.Is(err, files.ErrSharePasswordRequired):
					statusCode = http.StatusUnauthorized
					message = "share password required"
				}

				writeJSON(w, statusCode, publicFileResponse{
					Status:           strings.ToLower(strings.ReplaceAll(http.StatusText(statusCode), " ", "_")),
					PasswordRequired: errors.Is(err, files.ErrSharePasswordRequired),
					Message:          message,
					Timestamp:        time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			writeJSON(w, http.StatusOK, publicFileResponse{
				Status:    "ok",
				Item:      &item,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		})

		r.Get("/public/files/{publicID}", func(w http.ResponseWriter, r *http.Request) {
			publicID := chi.URLParam(r, "publicID")
			item, err := fileService.GetPublicFile(r.Context(), publicID, publicAccessTokenFromRequest(r, cfg, publicID))
			if err != nil {
				statusCode := http.StatusInternalServerError
				message := "failed to load public file"
				passwordRequired := false

				switch {
				case errors.Is(err, files.ErrNotFound):
					statusCode = http.StatusNotFound
					message = "file not found"
				case errors.Is(err, files.ErrSharePasswordRequired):
					statusCode = http.StatusUnauthorized
					message = "share password required"
					passwordRequired = true
				case errors.Is(err, files.ErrShareExpired):
					statusCode = http.StatusGone
					message = "share link expired"
				case errors.Is(err, files.ErrShareDownloadLimitReached):
					statusCode = http.StatusGone
					message = "download limit reached"
				}

				writeJSON(w, statusCode, publicFileResponse{
					Status:           strings.ToLower(strings.ReplaceAll(http.StatusText(statusCode), " ", "_")),
					PasswordRequired: passwordRequired,
					Message:          message,
					Timestamp:        time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			writeJSON(w, http.StatusOK, publicFileResponse{
				Status:    "ok",
				Item:      &item,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		})
	})

	handlePublicDownload := func(w http.ResponseWriter, r *http.Request) {
		publicID := chi.URLParam(r, "publicID")
		info, err := fileService.GetPublicDownload(r.Context(), publicID, publicAccessTokenFromRequest(r, cfg, publicID))
		if err != nil {
			statusCode := http.StatusInternalServerError
			message := "failed to resolve download"

			switch {
			case errors.Is(err, files.ErrNotFound):
				statusCode = http.StatusNotFound
				message = "file not found"
			case errors.Is(err, files.ErrSharePasswordRequired):
				statusCode = http.StatusUnauthorized
				message = "share password required"
			case errors.Is(err, files.ErrShareExpired):
				statusCode = http.StatusGone
				message = "share link expired"
			case errors.Is(err, files.ErrShareDownloadLimitReached):
				statusCode = http.StatusGone
				message = "download limit reached"
			}

			writeJSON(w, statusCode, statusResponse{
				Status:    strings.ToLower(strings.ReplaceAll(http.StatusText(statusCode), " ", "_")),
				Message:   message,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
			return
		}

		fileHandle, err := store.Open(r.Context(), info.StorageKey)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, statusResponse{
				Status:    "error",
				Message:   "failed to open file",
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
			return
		}
		defer fileHandle.Close()

		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": info.FileName})
		if disposition != "" {
			w.Header().Set("Content-Disposition", disposition)
		}
		if info.ContentType != "" {
			w.Header().Set("Content-Type", info.ContentType)
		}
		if info.SHA256 != "" {
			w.Header().Set("ETag", `"`+info.SHA256+`"`)
		}

		if err := fileService.RecordDownload(
			r.Context(),
			info.FileID,
			r.RemoteAddr,
			r.UserAgent(),
			r.Referer(),
		); err != nil {
			if errors.Is(err, files.ErrShareDownloadLimitReached) {
				writeJSON(w, http.StatusGone, statusResponse{
					Status:    "gone",
					Message:   "download limit reached",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			logger.Warn("failed to record download event", slog.String("error", err.Error()), slog.Int64("file_id", info.FileID))
		}

		logAuditEvent(r.Context(), audit.Event{
			Action:          "public_download",
			FileID:          &info.FileID,
			FilePublicID:    info.PublicID,
			FileDisplayName: info.FileName,
			Metadata: map[string]any{
				"contentType": info.ContentType,
				"size":        info.Size,
				"referer":     r.Referer(),
			},
			IPAddress: r.RemoteAddr,
			UserAgent: r.UserAgent(),
		})

		http.ServeContent(w, r, info.FileName, info.UpdatedAt, fileHandle)
	}

	router.Get("/f/{publicID}/{filename}", handlePublicDownload)
	router.Get("/f/{publicID}", handlePublicDownload)
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, statusResponse{
			Status:    "not_found",
			Message:   "route not found",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	})

	logger.Info("http router initialized", slog.Any("allowed_origins", cfg.AllowedOrigins))

	return router
}

func requireAuth(authService *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, err := authService.CurrentUser(r.Context(), authService.SessionTokenFromRequest(r))
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, authResponse{
					Status:    "unauthorized",
					Message:   "authentication required",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			next.ServeHTTP(w, auth.WithUser(r, user))
		})
	}
}

func logAuditEventForUser(r *http.Request, writer func(context.Context, audit.Event), event audit.Event) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		return
	}

	event.ActorUserID = &user.ID
	event.ActorUsername = user.Username
	writer(r.Context(), event)
}

func collectFileNames(items []files.FileRecord) []string {
	if len(items) == 0 {
		return nil
	}

	names := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.DisplayName) == "" {
			continue
		}
		names = append(names, item.DisplayName)
	}

	return names
}

func writeAuditLogsCSV(w http.ResponseWriter, items []audit.Record) error {
	buffer := &bytes.Buffer{}
	writer := csv.NewWriter(buffer)

	if err := writer.Write([]string{
		"created_at",
		"action",
		"actor_username",
		"actor_user_id",
		"file_display_name",
		"file_public_id",
		"file_id",
		"ip_hash",
		"user_agent",
		"metadata_json",
	}); err != nil {
		return err
	}

	for _, item := range items {
		metadataJSON := "{}"
		if len(item.Metadata) > 0 {
			encoded, err := json.Marshal(item.Metadata)
			if err != nil {
				return err
			}
			metadataJSON = string(encoded)
		}

		if err := writer.Write([]string{
			item.CreatedAt.UTC().Format(time.RFC3339),
			item.Action,
			item.ActorUsername,
			formatOptionalInt64(item.ActorUserID),
			item.FileDisplayName,
			item.FilePublicID,
			formatOptionalInt64(item.FileID),
			item.IPHash,
			item.UserAgent,
			metadataJSON,
		}); err != nil {
			return err
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}

	filename := "sharepier-audit-logs-" + time.Now().UTC().Format("20060102-150405") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(buffer.Bytes())
	return err
}

func formatOptionalInt64(value *int64) string {
	if value == nil {
		return ""
	}

	return strconv.FormatInt(*value, 10)
}

func notImplemented(message string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotImplemented, statusResponse{
			Status:    "not_implemented",
			Message:   message,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}
}

func parseInt64Param(r *http.Request, key string) (int64, error) {
	value := strings.TrimSpace(chi.URLParam(r, key))
	if value == "" {
		return 0, errors.New("missing param")
	}

	return strconv.ParseInt(value, 10, 64)
}

func publicAccessTokenFromRequest(r *http.Request, cfg config.Config, publicID string) string {
	cookie, err := r.Cookie(publicAccessCookieName(cfg.ShareAccessCookie, publicID))
	if err != nil {
		return ""
	}

	return cookie.Value
}

func setPublicAccessCookie(w http.ResponseWriter, cfg config.Config, publicID, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     publicAccessCookieName(cfg.ShareAccessCookie, publicID),
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
	})
}

func publicAccessCookieName(baseName, publicID string) string {
	baseName = strings.TrimSpace(baseName)
	if baseName == "" {
		baseName = "sharepier_public_access"
	}

	return baseName + "_" + strings.TrimSpace(publicID)
}
