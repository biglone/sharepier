package httpx

import (
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

	"sharepier-api/internal/auth"
	"sharepier-api/internal/config"
	"sharepier-api/internal/files"
	"sharepier-api/internal/storage"
)

type statusResponse struct {
	Status      string `json:"status"`
	Name        string `json:"name,omitempty"`
	Version     string `json:"version,omitempty"`
	Environment string `json:"environment,omitempty"`
	Timestamp   string `json:"timestamp,omitempty"`
	Message     string `json:"message,omitempty"`
	StorageRoot string `json:"storageRoot,omitempty"`
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

func NewRouter(
	cfg config.Config,
	logger *slog.Logger,
	store *storage.LocalFSStore,
	authService *auth.Service,
	fileService *files.Service,
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
			Status:      "ok",
			Name:        cfg.AppName,
			Version:     "0.1.0-skeleton",
			Environment: cfg.Env,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			StorageRoot: store.Root(),
		})
	})

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
			writeJSON(w, http.StatusOK, authResponse{
				Status:    "ok",
				User:      &user,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		})

		r.Post("/auth/logout", func(w http.ResponseWriter, r *http.Request) {
			token := authService.SessionTokenFromRequest(r)
			if err := authService.Logout(r.Context(), token); err != nil {
				writeJSON(w, http.StatusInternalServerError, authResponse{
					Status:    "error",
					Message:   "logout failed",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			authService.ClearSessionCookie(w)
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
				Reader:       uploadedFile,
			})
			if err != nil {
				status := http.StatusInternalServerError
				message := "upload failed"
				if errors.Is(err, files.ErrInvalidUpload) {
					status = http.StatusBadRequest
					message = err.Error()
				}

				writeJSON(w, status, filesResponse{
					Status:    "error",
					Message:   message,
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

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

		fileHandle, err := store.Open(info.StorageKey)
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
