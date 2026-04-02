package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"
)

type Service struct {
	db *sql.DB
}

type Event struct {
	ActorUserID     *int64
	ActorUsername   string
	Action          string
	FileID          *int64
	FilePublicID    string
	FileDisplayName string
	Metadata        map[string]any
	IPAddress       string
	UserAgent       string
}

type ListParams struct {
	Limit  int
	Action string
	Query  string
	From   *time.Time
	To     *time.Time
}

type Record struct {
	ID              int64          `json:"id"`
	ActorUserID     *int64         `json:"actorUserId,omitempty"`
	ActorUsername   string         `json:"actorUsername,omitempty"`
	Action          string         `json:"action"`
	FileID          *int64         `json:"fileId,omitempty"`
	FilePublicID    string         `json:"filePublicId,omitempty"`
	FileDisplayName string         `json:"fileDisplayName,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	IPHash          string         `json:"ipHash,omitempty"`
	UserAgent       string         `json:"userAgent,omitempty"`
	CreatedAt       time.Time      `json:"createdAt"`
}

func NewService(database *sql.DB) *Service {
	return &Service{db: database}
}

func (s *Service) Log(ctx context.Context, event Event) error {
	action := strings.TrimSpace(strings.ToLower(event.Action))
	if action == "" {
		return nil
	}

	metadataBytes := []byte(`{}`)
	if len(event.Metadata) > 0 {
		encoded, err := json.Marshal(event.Metadata)
		if err != nil {
			return err
		}
		metadataBytes = encoded
	}

	var actorUserID any
	if event.ActorUserID != nil && *event.ActorUserID > 0 {
		actorUserID = *event.ActorUserID
	}

	var fileID any
	if event.FileID != nil && *event.FileID > 0 {
		fileID = *event.FileID
	}

	_, err := s.db.ExecContext(
		ctx,
		`insert into audit_logs (
			actor_user_id,
			actor_username,
			action,
			file_id,
			file_public_id,
			file_display_name,
			metadata,
			ip_hash,
			user_agent
		) values ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9)`,
		actorUserID,
		strings.TrimSpace(event.ActorUsername),
		action,
		fileID,
		strings.TrimSpace(event.FilePublicID),
		strings.TrimSpace(event.FileDisplayName),
		string(metadataBytes),
		hashString(normalizeIPAddress(event.IPAddress)),
		strings.TrimSpace(event.UserAgent),
	)
	return err
}

func (s *Service) List(ctx context.Context, params ListParams) ([]Record, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	action := strings.TrimSpace(strings.ToLower(params.Action))
	query := strings.TrimSpace(strings.ToLower(params.Query))

	conditions := make([]string, 0, 4)
	args := make([]any, 0, 5)

	if action != "" {
		args = append(args, action)
		conditions = append(conditions, fmt.Sprintf("action = $%d", len(args)))
	}
	if query != "" {
		args = append(args, "%"+query+"%")
		index := len(args)
		conditions = append(conditions, fmt.Sprintf(`(
			lower(coalesce(actor_username, '')) like $%[1]d or
			lower(coalesce(action, '')) like $%[1]d or
			lower(coalesce(file_public_id, '')) like $%[1]d or
			lower(coalesce(file_display_name, '')) like $%[1]d
		)`, index))
	}
	if params.From != nil {
		args = append(args, params.From.UTC())
		conditions = append(conditions, fmt.Sprintf("created_at >= $%d", len(args)))
	}
	if params.To != nil {
		args = append(args, params.To.UTC())
		conditions = append(conditions, fmt.Sprintf("created_at <= $%d", len(args)))
	}

	queryBuilder := strings.Builder{}
	queryBuilder.WriteString(`select
			id,
			actor_user_id,
			actor_username,
			action,
			file_id,
			file_public_id,
			file_display_name,
			metadata,
			ip_hash,
			user_agent,
			created_at
		from audit_logs`)
	if len(conditions) > 0 {
		queryBuilder.WriteString("\nwhere ")
		queryBuilder.WriteString(strings.Join(conditions, "\n  and "))
	}
	args = append(args, limit)
	queryBuilder.WriteString(fmt.Sprintf("\norder by created_at desc, id desc\nlimit $%d", len(args)))

	rows, err := s.db.QueryContext(
		ctx,
		queryBuilder.String(),
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Record, 0, limit)
	for rows.Next() {
		var (
			item            Record
			actorUserID     sql.NullInt64
			fileID          sql.NullInt64
			actorUsername   sql.NullString
			filePublicID    sql.NullString
			fileDisplayName sql.NullString
			metadataBytes   []byte
			ipHash          sql.NullString
			userAgent       sql.NullString
		)
		if err := rows.Scan(
			&item.ID,
			&actorUserID,
			&actorUsername,
			&item.Action,
			&fileID,
			&filePublicID,
			&fileDisplayName,
			&metadataBytes,
			&ipHash,
			&userAgent,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}

		if actorUserID.Valid {
			value := actorUserID.Int64
			item.ActorUserID = &value
		}
		if fileID.Valid {
			value := fileID.Int64
			item.FileID = &value
		}
		if actorUsername.Valid {
			item.ActorUsername = actorUsername.String
		}
		if filePublicID.Valid {
			item.FilePublicID = filePublicID.String
		}
		if fileDisplayName.Valid {
			item.FileDisplayName = fileDisplayName.String
		}
		if ipHash.Valid {
			item.IPHash = ipHash.String
		}
		if userAgent.Valid {
			item.UserAgent = userAgent.String
		}
		if len(metadataBytes) > 0 {
			if err := json.Unmarshal(metadataBytes, &item.Metadata); err != nil {
				return nil, err
			}
		}

		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func ValidActions() []string {
	return slices.Clone(validActions)
}

func IsValidAction(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return value == "" || slices.Contains(validActions, value)
}

func normalizeIPAddress(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	if strings.Contains(value, ",") {
		value = strings.TrimSpace(strings.Split(value, ",")[0])
	}

	host, _, err := net.SplitHostPort(value)
	if err == nil {
		return host
	}

	return strings.Trim(value, "[]")
}

func hashString(value string) string {
	if value == "" {
		return ""
	}

	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

var validActions = []string{
	"auth_login",
	"auth_login_failed",
	"auth_logout",
	"file_upload",
	"file_status_change",
	"file_delete",
	"file_batch_set_status",
	"file_batch_delete",
	"file_share_policy_update",
	"public_download",
}
