package state

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteStore is a SQLite-backed implementation of the Store interface.
type SQLiteStore struct {
	db *sql.DB
}

// Compile-time check that SQLiteStore implements Store.
var _ Store = (*SQLiteStore)(nil)

// NewSQLiteStore opens a SQLite database at the given DSN and initializes the schema.
//
// Use ":memory:" for an ephemeral in-memory store. For on-disk databases the DSN
// is the file path.
func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite database: %w", err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set pragmas: %w", err)
	}

	// NOTE: In-memory databases are private to each connection opened by sql.DB.
	// Limit the pool to a single connection so all operations share the same DB.
	if dsn == ":memory:" {
		db.SetMaxOpenConns(1)
	}

	store := &SQLiteStore{db: db}
	if err := store.createSchema(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	return store, nil
}

// Close releases the underlying database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) createSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY,
		current_state TEXT NOT NULL,
		retry_count INTEGER NOT NULL DEFAULT 0,
		max_retries INTEGER NOT NULL,
		validation_decision TEXT,
		final_output TEXT,
		context_snapshot TEXT
	);

	CREATE TABLE IF NOT EXISTS state_transitions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL,
		from_state TEXT NOT NULL,
		to_state TEXT NOT NULL,
		occurred_at INTEGER NOT NULL,
		FOREIGN KEY (task_id) REFERENCES tasks(id)
	);

	CREATE INDEX IF NOT EXISTS idx_state_transitions_task_id ON state_transitions(task_id);

	CREATE TABLE IF NOT EXISTS validation_decisions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL,
		pass INTEGER NOT NULL,
		feedback TEXT NOT NULL,
		recorded_at INTEGER NOT NULL,
		FOREIGN KEY (task_id) REFERENCES tasks(id)
	);

	CREATE INDEX IF NOT EXISTS idx_validation_decisions_task_id ON validation_decisions(task_id);

	CREATE TABLE IF NOT EXISTS tool_calls (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL,
		call TEXT NOT NULL,
		args TEXT NOT NULL DEFAULT '',
		response TEXT NOT NULL DEFAULT '',
		recorded_at INTEGER NOT NULL,
		FOREIGN KEY (task_id) REFERENCES tasks(id)
	);

	CREATE INDEX IF NOT EXISTS idx_tool_calls_task_id ON tool_calls(task_id);
	`

	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("execute schema: %w", err)
	}

	// NOTE: In-place migration for existing databases that were created
	// before the args/response columns existed. SQLite silently ignores
	// duplicate column errors, so this is safe to run on fresh databases.
	for _, col := range []string{"args", "response"} {
		// SQLite does not support IF NOT EXISTS for ALTER TABLE ADD COLUMN;
		// the error on duplicate column is harmless, so we ignore it.
		_, _ = s.db.Exec("ALTER TABLE tool_calls ADD COLUMN " + col + " TEXT NOT NULL DEFAULT ''")
	}

	return nil
}

// CreateTask initializes a task in the CREATED state.
func (s *SQLiteStore) CreateTask(id string, maxRetries int) (*Task, error) {
	if maxRetries < 0 {
		return nil, errors.New("max retries cannot be negative")
	}

	_, err := s.db.Exec(
		"INSERT INTO tasks (id, current_state, retry_count, max_retries) VALUES (?, ?, ?, ?)",
		id, StateCreated, 0, maxRetries,
	)
	if err != nil {
		if isUniqueConstraintError(err) {
			return nil, ErrTaskAlreadyExists
		}
		return nil, fmt.Errorf("insert task: %w", err)
	}

	return &Task{
		ID:           id,
		CurrentState: StateCreated,
		RetryCount:   0,
		MaxRetries:   maxRetries,
	}, nil
}

// GetTask retrieves the current state of a task.
func (s *SQLiteStore) GetTask(id string) (*Task, error) {
	row := s.db.QueryRow(`
		SELECT id, current_state, retry_count, max_retries,
		       COALESCE(validation_decision, ''),
		       COALESCE(final_output, ''),
		       COALESCE(context_snapshot, '')
		FROM tasks
		WHERE id = ?`,
		id,
	)

	task := &Task{}
	if err := row.Scan(
		&task.ID,
		&task.CurrentState,
		&task.RetryCount,
		&task.MaxRetries,
		&task.ValidationDecision,
		&task.FinalOutput,
		&task.ContextSnapshot,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTaskNotFound
		}
		return nil, fmt.Errorf("scan task: %w", err)
	}

	return task, nil
}

// TransitionState attempts to transition the task to a new state.
func (s *SQLiteStore) TransitionState(id string, newState TaskState) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var current TaskState
	if err := tx.QueryRow("SELECT current_state FROM tasks WHERE id = ?", id).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTaskNotFound
		}
		return fmt.Errorf("select current state: %w", err)
	}

	if !IsValidTransition(current, newState) {
		return ErrInvalidTransition
	}

	if _, err := tx.Exec("UPDATE tasks SET current_state = ? WHERE id = ?", newState, id); err != nil {
		return fmt.Errorf("update state: %w", err)
	}

	if _, err := tx.Exec(
		"INSERT INTO state_transitions (task_id, from_state, to_state, occurred_at) VALUES (?, ?, ?, ?)",
		id, current, newState, time.Now().Unix(),
	); err != nil {
		return fmt.Errorf("insert state transition: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transition: %w", err)
	}
	return nil
}

// IncrementRetry increments the retry counter for the task.
func (s *SQLiteStore) IncrementRetry(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var current TaskState
	var retryCount, maxRetries int
	if err := tx.QueryRow(
		"SELECT current_state, retry_count, max_retries FROM tasks WHERE id = ?",
		id,
	).Scan(&current, &retryCount, &maxRetries); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTaskNotFound
		}
		return fmt.Errorf("select task: %w", err)
	}

	// NOTE: Terminal states may never be retried.
	if current == StateFailedEscalated || current == StateCommitted {
		return ErrInvalidTransition
	}

	if current != StateRevisionRequested {
		return ErrInvalidTransition
	}

	if retryCount >= maxRetries {
		return ErrMaxRetriesReached
	}

	if _, err := tx.Exec("UPDATE tasks SET retry_count = retry_count + 1 WHERE id = ?", id); err != nil {
		return fmt.Errorf("increment retry: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit increment: %w", err)
	}
	return nil
}

// RecordToolCall persists a tool call made while processing the task.
func (s *SQLiteStore) RecordToolCall(id, call, args, response string) error {
	var dummy int
	if err := s.db.QueryRow("SELECT 1 FROM tasks WHERE id = ?", id).Scan(&dummy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTaskNotFound
		}
		return fmt.Errorf("check task existence: %w", err)
	}

	_, err := s.db.Exec(
		"INSERT INTO tool_calls (task_id, call, args, response, recorded_at) VALUES (?, ?, ?, ?, ?)",
		id, call, args, response, time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("insert tool call: %w", err)
	}
	return nil
}

// GetToolCalls returns all tool calls recorded for the task in insertion order.
func (s *SQLiteStore) GetToolCalls(id string) ([]ToolCall, error) {
	rows, err := s.db.Query(
		"SELECT id, task_id, COALESCE(call,''), COALESCE(args,''), COALESCE(response,'') FROM tool_calls WHERE task_id = ? ORDER BY id ASC",
		id,
	)
	if err != nil {
		return nil, fmt.Errorf("query tool calls: %w", err)
	}
	defer rows.Close()

	var calls []ToolCall
	for rows.Next() {
		var tc ToolCall
		if err := rows.Scan(&tc.ID, &tc.TaskID, &tc.Call, &tc.Args, &tc.Response); err != nil {
			return nil, fmt.Errorf("scan tool call: %w", err)
		}
		calls = append(calls, tc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tool calls: %w", err)
	}
	return calls, nil
}

// validationDecision is the persisted shape of RecordValidationDecision data.
type validationDecision struct {
	Pass     bool   `json:"pass"`
	Feedback string `json:"feedback"`
}

// RecordValidationDecision persists the validation decision and feedback.
// It appends to the validation_decisions history table so that every signal
// in a retry loop remains reproducible, and also updates the tasks row with
// the latest decision for convenience.
func (s *SQLiteStore) RecordValidationDecision(id string, pass bool, feedback string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var dummy int
	if err := tx.QueryRow("SELECT 1 FROM tasks WHERE id = ?", id).Scan(&dummy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTaskNotFound
		}
		return fmt.Errorf("check task existence: %w", err)
	}

	if _, err := tx.Exec(
		"INSERT INTO validation_decisions (task_id, pass, feedback, recorded_at) VALUES (?, ?, ?, ?)",
		id, pass, feedback, time.Now().Unix(),
	); err != nil {
		return fmt.Errorf("insert validation decision: %w", err)
	}

	decision := validationDecision{Pass: pass, Feedback: feedback}
	data, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("marshal validation decision: %w", err)
	}

	if _, err := tx.Exec(
		"UPDATE tasks SET validation_decision = ? WHERE id = ?",
		string(data), id,
	); err != nil {
		return fmt.Errorf("update validation decision: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit validation decision: %w", err)
	}
	return nil
}

// RecordFinalOutput persists the final output produced for the task.
//
// NOTE: Wrapped in an explicit transaction so the write is atomic and
// no reader can observe a partially-written final_output column.
func (s *SQLiteStore) RecordFinalOutput(id, output string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var dummy int
	if err := tx.QueryRow("SELECT 1 FROM tasks WHERE id = ?", id).Scan(&dummy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTaskNotFound
		}
		return fmt.Errorf("check task existence: %w", err)
	}

	if _, err := tx.Exec(
		"UPDATE tasks SET final_output = ? WHERE id = ?",
		output, id,
	); err != nil {
		return fmt.Errorf("update final output: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit final output: %w", err)
	}
	return nil
}

// RecordContextSnapshot persists the context snapshot for the task.
//
// NOTE: Wrapped in an explicit transaction so the write is atomic and
// no reader can observe a partially-written context_snapshot column.
func (s *SQLiteStore) RecordContextSnapshot(id string, snapshot string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var dummy int
	if err := tx.QueryRow("SELECT 1 FROM tasks WHERE id = ?", id).Scan(&dummy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTaskNotFound
		}
		return fmt.Errorf("check task existence: %w", err)
	}

	if _, err := tx.Exec(
		"UPDATE tasks SET context_snapshot = ? WHERE id = ?",
		snapshot, id,
	); err != nil {
		return fmt.Errorf("update context snapshot: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit context snapshot: %w", err)
	}
	return nil
}

// StateTransition records a single task state change for replay/audit.
type StateTransition struct {
	ID         int64
	TaskID     string
	FromState  TaskState
	ToState    TaskState
	OccurredAt int64
}

// GetStateTransitions returns the full ordered history of state transitions for a task.
func (s *SQLiteStore) GetStateTransitions(id string) ([]StateTransition, error) {
	rows, err := s.db.Query(
		"SELECT id, task_id, from_state, to_state, occurred_at FROM state_transitions WHERE task_id = ? ORDER BY id ASC",
		id,
	)
	if err != nil {
		return nil, fmt.Errorf("query state transitions: %w", err)
	}
	defer rows.Close()

	var transitions []StateTransition
	for rows.Next() {
		var st StateTransition
		if err := rows.Scan(&st.ID, &st.TaskID, &st.FromState, &st.ToState, &st.OccurredAt); err != nil {
			return nil, fmt.Errorf("scan state transition: %w", err)
		}
		transitions = append(transitions, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate state transitions: %w", err)
	}
	return transitions, nil
}

// ValidationDecisionRecord is a single persisted validation signal.
type ValidationDecisionRecord struct {
	ID         int64
	TaskID     string
	Pass       bool
	Feedback   string
	RecordedAt int64
}

// GetValidationDecisions returns the full ordered history of validation decisions for a task.
func (s *SQLiteStore) GetValidationDecisions(id string) ([]ValidationDecisionRecord, error) {
	rows, err := s.db.Query(
		"SELECT id, task_id, pass, feedback, recorded_at FROM validation_decisions WHERE task_id = ? ORDER BY id ASC",
		id,
	)
	if err != nil {
		return nil, fmt.Errorf("query validation decisions: %w", err)
	}
	defer rows.Close()

	var decisions []ValidationDecisionRecord
	for rows.Next() {
		var vd ValidationDecisionRecord
		if err := rows.Scan(&vd.ID, &vd.TaskID, &vd.Pass, &vd.Feedback, &vd.RecordedAt); err != nil {
			return nil, fmt.Errorf("scan validation decision: %w", err)
		}
		decisions = append(decisions, vd)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate validation decisions: %w", err)
	}
	return decisions, nil
}

// isUniqueConstraintError detects SQLite UNIQUE constraint violations.
//
// NOTE: modernc.org/sqlite error constants vary by version, so we match the
// canonical error message text as a stable fallback.
func isUniqueConstraintError(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
