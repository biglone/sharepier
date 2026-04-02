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
	Status    string             `json:"status"`
	Items     []files.FileRecord `json:"items,omitempty"`
	Item      *files.FileRecord  `json:"item,omitempty"`
	Message   string             `json:"message,omitempty"`
	Timestamp string             `json:"timestamp,omitempty"`
}

type publicFileResponse struct {
	Status    string                  `json:"status"`
	Item      *files.PublicFileRecord `json:"item,omitempty"`
	Message   string                  `json:"message,omitempty"`
	Timestamp string                  `json:"timestamp,omitempty"`
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

		r.Get("/public/files/{publicID}", func(w http.ResponseWriter, r *http.Request) {
			item, err := fileService.GetPublicFile(r.Context(), chi.URLParam(r, "publicID"))
			if err != nil {
				statusCode := http.StatusInternalServerError
				message := "failed to load public file"
				if errors.Is(err, files.ErrNotFound) {
					statusCode = http.StatusNotFound
					message = "file not found"
				}

				writeJSON(w, statusCode, publicFileResponse{
					Status:    strings.ToLower(strings.ReplaceAll(http.StatusText(statusCode), " ", "_")),
					Message:   message,
					Timestamp: time.Now().UTC().Format(time.RFC3339),
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
		info, err := fileService.GetPublicDownload(r.Context(), chi.URLParam(r, "publicID"))
		if err != nil {
			if errors.Is(err, files.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, statusResponse{
					Status:    "not_found",
					Message:   "file not found",
					Timestamp: time.Now().UTC().Format(time.RFC3339),
				})
				return
			}

			writeJSON(w, http.StatusInternalServerError, statusResponse{
				Status:    "error",
				Message:   "failed to resolve download",
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
