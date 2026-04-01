package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/eemax/tinyflags/internal/core"
	cerr "github.com/eemax/tinyflags/internal/errors"
)

type SessionStore interface {
	LoadOrCreate(name string) (core.Session, error)
	Fork(sourceName, forkName string) (core.Session, error)
	AppendMessages(sessionID int64, runID *int64, msgs []core.Message) error
	GetMessages(sessionID int64) ([]core.Message, error)
}

type AdminStore interface {
	SessionStore
	List() ([]core.Session, error)
	Show(name string) (core.Session, []core.StoredMessage, error)
	Delete(name string) error
	Clear(name string) error
	Export(name string) (core.SessionExport, error)
}

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

func (s *SQLiteStore) LoadOrCreate(name string) (core.Session, error) {
	if session, err := s.lookup(name); err == nil {
		return session, nil
	} else if !isNotFound(err) {
		return core.Session{}, err
	}
	now := time.Now().UTC()
	result, err := s.db.Exec(`INSERT INTO sessions (name, created_at, updated_at) VALUES (?, ?, ?)`, name, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return core.Session{}, cerr.Wrap(cerr.ExitSessionFailure, "create session", err)
	}
	id, _ := result.LastInsertId()
	return core.Session{ID: id, Name: name, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *SQLiteStore) Fork(sourceName, forkName string) (core.Session, error) {
	source, err := s.lookup(sourceName)
	if err != nil {
		if isNotFound(err) {
			return core.Session{}, cerr.New(cerr.ExitCLIUsage, fmt.Sprintf("session %q not found", sourceName))
		}
		return core.Session{}, err
	}
	if _, err := s.lookup(forkName); err == nil {
		return core.Session{}, cerr.New(cerr.ExitCLIUsage, fmt.Sprintf("session %q already exists", forkName))
	} else if !isNotFound(err) {
		return core.Session{}, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return core.Session{}, cerr.Wrap(cerr.ExitSessionFailure, "start session fork transaction", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	result, err := tx.Exec(`INSERT INTO sessions (name, created_at, updated_at) VALUES (?, ?, ?)`, forkName, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return core.Session{}, cerr.Wrap(cerr.ExitSessionFailure, "create fork session", err)
	}
	forkID, _ := result.LastInsertId()

	rows, err := tx.Query(`SELECT role, content, tool_name, name, tool_call_id, tool_calls_json, created_at FROM messages WHERE session_id = ? ORDER BY id`, source.ID)
	if err != nil {
		return core.Session{}, cerr.Wrap(cerr.ExitSessionFailure, "load source session messages", err)
	}
	defer rows.Close()

	for rows.Next() {
		var role, content string
		var toolName, name, toolCallID sql.NullString
		var toolCallsJSON sql.NullString
		var createdAt string
		if err := rows.Scan(&role, &content, &toolName, &name, &toolCallID, &toolCallsJSON, &createdAt); err != nil {
			return core.Session{}, cerr.Wrap(cerr.ExitSessionFailure, "scan source session messages", err)
		}
		if _, err := tx.Exec(
			`INSERT INTO messages (session_id, role, content, tool_name, name, tool_call_id, tool_calls_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			forkID, role, content, nullStringValue(toolName), nullStringValue(name), nullStringValue(toolCallID), nullStringValue(toolCallsJSON), createdAt,
		); err != nil {
			return core.Session{}, cerr.Wrap(cerr.ExitSessionFailure, "copy session message", err)
		}
	}
	if err := rows.Err(); err != nil {
		return core.Session{}, cerr.Wrap(cerr.ExitSessionFailure, "iterate source session messages", err)
	}
	if err := tx.Commit(); err != nil {
		return core.Session{}, cerr.Wrap(cerr.ExitSessionFailure, "commit session fork", err)
	}
	return core.Session{ID: forkID, Name: forkName, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *SQLiteStore) AppendMessages(sessionID int64, runID *int64, msgs []core.Message) error {
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return cerr.Wrap(cerr.ExitSessionFailure, "begin append messages", err)
	}
	defer tx.Rollback()

	for _, msg := range msgs {
		var runValue any
		if runID != nil {
			runValue = *runID
		}
		var toolCallsJSON any
		if len(msg.ToolCalls) > 0 {
			data, _ := json.Marshal(msg.ToolCalls)
			toolCallsJSON = string(data)
		}
		var toolName any
		if msg.Role == "tool" {
			toolName = msg.Name
		}
		if _, err := tx.Exec(
			`INSERT INTO messages (session_id, run_id, role, content, tool_name, name, tool_call_id, tool_calls_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sessionID, runValue, msg.Role, msg.Content, toolName, nullable(msg.Name), nullable(msg.ToolCallID), toolCallsJSON, now.Format(time.RFC3339Nano),
		); err != nil {
			return cerr.Wrap(cerr.ExitSessionFailure, "append message", err)
		}
	}
	if _, err := tx.Exec(`UPDATE sessions SET updated_at = ? WHERE id = ?`, now.Format(time.RFC3339Nano), sessionID); err != nil {
		return cerr.Wrap(cerr.ExitSessionFailure, "touch session", err)
	}
	if err := tx.Commit(); err != nil {
		return cerr.Wrap(cerr.ExitSessionFailure, "commit messages", err)
	}
	return nil
}

func (s *SQLiteStore) GetMessages(sessionID int64) ([]core.Message, error) {
	rows, err := s.db.Query(`SELECT role, content, name, tool_call_id, tool_calls_json FROM messages WHERE session_id = ? ORDER BY id`, sessionID)
	if err != nil {
		return nil, cerr.Wrap(cerr.ExitSessionFailure, "load session messages", err)
	}
	defer rows.Close()

	out := []core.Message{}
	for rows.Next() {
		var msg core.Message
		var name, toolCallID, toolCallsJSON sql.NullString
		if err := rows.Scan(&msg.Role, &msg.Content, &name, &toolCallID, &toolCallsJSON); err != nil {
			return nil, cerr.Wrap(cerr.ExitSessionFailure, "scan session messages", err)
		}
		msg.Name = nullStringValue(name)
		msg.ToolCallID = nullStringValue(toolCallID)
		if toolCallsJSON.Valid && toolCallsJSON.String != "" {
			_ = json.Unmarshal([]byte(toolCallsJSON.String), &msg.ToolCalls)
		}
		out = append(out, msg)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) List() ([]core.Session, error) {
	rows, err := s.db.Query(`SELECT id, name, created_at, updated_at FROM sessions ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, cerr.Wrap(cerr.ExitSessionFailure, "list sessions", err)
	}
	defer rows.Close()
	out := []core.Session{}
	for rows.Next() {
		var item core.Session
		var createdAt, updatedAt string
		if err := rows.Scan(&item.ID, &item.Name, &createdAt, &updatedAt); err != nil {
			return nil, cerr.Wrap(cerr.ExitSessionFailure, "scan sessions", err)
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) Show(name string) (core.Session, []core.StoredMessage, error) {
	session, err := s.lookup(name)
	if err != nil {
		return core.Session{}, nil, err
	}
	rows, err := s.db.Query(`SELECT id, run_id, role, content, tool_name, name, tool_call_id, tool_calls_json, created_at FROM messages WHERE session_id = ? ORDER BY id`, session.ID)
	if err != nil {
		return core.Session{}, nil, cerr.Wrap(cerr.ExitSessionFailure, "show session", err)
	}
	defer rows.Close()
	out := []core.StoredMessage{}
	for rows.Next() {
		var item core.StoredMessage
		var runID sql.NullInt64
		var toolName, nameValue, toolCallID, toolCallsJSON sql.NullString
		var createdAt string
		if err := rows.Scan(&item.ID, &runID, &item.Role, &item.Content, &toolName, &nameValue, &toolCallID, &toolCallsJSON, &createdAt); err != nil {
			return core.Session{}, nil, cerr.Wrap(cerr.ExitSessionFailure, "scan session message", err)
		}
		item.SessionID = session.ID
		if runID.Valid {
			id := runID.Int64
			item.RunID = &id
		}
		item.ToolName = nullStringValue(toolName)
		item.Name = nullStringValue(nameValue)
		item.ToolID = nullStringValue(toolCallID)
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		if toolCallsJSON.Valid && toolCallsJSON.String != "" {
			_ = json.Unmarshal([]byte(toolCallsJSON.String), &item.ToolCalls)
		}
		out = append(out, item)
	}
	return session, out, rows.Err()
}

func (s *SQLiteStore) Delete(name string) error {
	result, err := s.db.Exec(`DELETE FROM sessions WHERE name = ?`, name)
	if err != nil {
		return cerr.Wrap(cerr.ExitSessionFailure, "delete session", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return cerr.New(cerr.ExitCLIUsage, fmt.Sprintf("session %q not found", name))
	}
	return nil
}

func (s *SQLiteStore) Clear(name string) error {
	session, err := s.lookup(name)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return cerr.Wrap(cerr.ExitSessionFailure, "begin clear session", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM messages WHERE session_id = ?`, session.ID); err != nil {
		return cerr.Wrap(cerr.ExitSessionFailure, "clear session messages", err)
	}
	if _, err := tx.Exec(`UPDATE sessions SET updated_at = ? WHERE id = ?`, now.Format(time.RFC3339Nano), session.ID); err != nil {
		return cerr.Wrap(cerr.ExitSessionFailure, "touch cleared session", err)
	}
	if err := tx.Commit(); err != nil {
		return cerr.Wrap(cerr.ExitSessionFailure, "commit clear session", err)
	}
	return nil
}

func (s *SQLiteStore) Export(name string) (core.SessionExport, error) {
	session, msgs, err := s.Show(name)
	if err != nil {
		return core.SessionExport{}, err
	}
	return core.SessionExport{Session: session, Messages: msgs}, nil
}

func (s *SQLiteStore) lookup(name string) (core.Session, error) {
	var item core.Session
	var createdAt, updatedAt string
	err := s.db.QueryRow(`SELECT id, name, created_at, updated_at FROM sessions WHERE name = ?`, name).Scan(&item.ID, &item.Name, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return core.Session{}, cerr.New(cerr.ExitCLIUsage, fmt.Sprintf("session %q not found", name))
		}
		return core.Session{}, cerr.Wrap(cerr.ExitSessionFailure, "lookup session", err)
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return item, nil
}

func isNotFound(err error) bool {
	typed, ok := err.(*cerr.ExitCodeError)
	return ok && typed.Code == cerr.ExitCLIUsage
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullStringValue(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}
