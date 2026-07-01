// Package database provides the SQLite-backed implementation of the srs
// domain's Repository interface.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/contre95/stella/src/srs"
	_ "github.com/mattn/go-sqlite3"
)

// SqliteSRS implements srs.Repository on top of SQLite.
type SqliteSRS struct {
	db *sql.DB
}

// Compile-time assertion that we satisfy the domain contract.
var _ srs.Repository = (*SqliteSRS)(nil)

// cardColumns is the fixed column list used by every card SELECT, matching the
// order scanCard expects.
const cardColumns = `id, fact_id, deck_id, card_view, grade, easiness,
	acq_reps, ret_reps, lapses, acq_reps_since_lapse, ret_reps_since_lapse,
	last_rep, next_rep, active`

// NewSqliteSRS opens (or creates) the database at path and ensures the schema.
func NewSqliteSRS(path string) (*SqliteSRS, error) {
	dsn := fmt.Sprintf("file:%s?_foreign_keys=on&_busy_timeout=5000", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	// Single-user app: one connection avoids SQLITE_BUSY on concurrent writes.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("connecting to database: %w", err)
	}
	s := &SqliteSRS{db: db}
	if err := s.createTables(); err != nil {
		return nil, err
	}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *SqliteSRS) Close() error { return s.db.Close() }

// migrate applies additive schema changes that cannot be expressed with
// CREATE TABLE IF NOT EXISTS (e.g., new columns on existing tables).
func (s *SqliteSRS) migrate() error {
	for _, col := range []string{"image_front", "image_back"} {
		_, err := s.db.Exec(`ALTER TABLE facts ADD COLUMN ` + col + ` TEXT NOT NULL DEFAULT ''`)
		if err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("migrating facts.%s: %w", col, err)
		}
	}
	return nil
}

func (s *SqliteSRS) createTables() error {
	const schema = `
CREATE TABLE IF NOT EXISTS decks (
	id   TEXT PRIMARY KEY,
	name TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS facts (
	id         TEXT PRIMARY KEY,
	front      TEXT NOT NULL,
	back       TEXT NOT NULL,
	image_front TEXT NOT NULL DEFAULT '',
	image_back  TEXT NOT NULL DEFAULT '',
	deck_id    TEXT NOT NULL DEFAULT '',
	created    INTEGER NOT NULL,
	modified   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS cards (
	id                   TEXT PRIMARY KEY,
	fact_id              TEXT NOT NULL,
	deck_id              TEXT NOT NULL DEFAULT '',
	card_view            TEXT NOT NULL,
	grade                INTEGER NOT NULL,
	easiness             REAL NOT NULL,
	acq_reps             INTEGER NOT NULL,
	ret_reps             INTEGER NOT NULL,
	lapses               INTEGER NOT NULL,
	acq_reps_since_lapse INTEGER NOT NULL,
	ret_reps_since_lapse INTEGER NOT NULL,
	last_rep             INTEGER NOT NULL,
	next_rep             INTEGER NOT NULL,
	active               INTEGER NOT NULL,
	FOREIGN KEY (fact_id) REFERENCES facts(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_cards_due  ON cards(active, grade, next_rep);
CREATE INDEX IF NOT EXISTS idx_cards_fact ON cards(fact_id);

CREATE TABLE IF NOT EXISTS reviews (
	id                 TEXT PRIMARY KEY,
	card_id            TEXT NOT NULL,
	grade              INTEGER NOT NULL,
	scheduled_interval INTEGER NOT NULL,
	actual_interval    INTEGER NOT NULL,
	thinking_time      INTEGER NOT NULL,
	timestamp          INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_reviews_card ON reviews(card_id);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("creating schema: %w", err)
	}
	return nil
}

//
// Facts.
//

func (s *SqliteSRS) AddFact(ctx context.Context, f *srs.Fact) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO facts (id, front, back, image_front, image_back, deck_id, created, modified)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.Front, f.Back, f.ImageFront, f.ImageBack, f.DeckID, f.Created, f.Modified)
	return err
}

func (s *SqliteSRS) GetFact(ctx context.Context, id string) (*srs.Fact, error) {
	var f srs.Fact
	err := s.db.QueryRowContext(ctx,
		`SELECT id, front, back, image_front, image_back, deck_id, created, modified FROM facts WHERE id=?`, id).
		Scan(&f.ID, &f.Front, &f.Back, &f.ImageFront, &f.ImageBack, &f.DeckID, &f.Created, &f.Modified)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, srs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (s *SqliteSRS) ListFacts(ctx context.Context) ([]*srs.Fact, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, front, back, image_front, image_back, deck_id, created, modified FROM facts ORDER BY created DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var facts []*srs.Fact
	for rows.Next() {
		var f srs.Fact
		if err := rows.Scan(&f.ID, &f.Front, &f.Back, &f.ImageFront, &f.ImageBack, &f.DeckID, &f.Created, &f.Modified); err != nil {
			return nil, err
		}
		facts = append(facts, &f)
	}
	return facts, rows.Err()
}

func (s *SqliteSRS) UpdateFact(ctx context.Context, f *srs.Fact) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE facts SET front=?, back=?, image_front=?, image_back=?, deck_id=?, modified=? WHERE id=?`,
		f.Front, f.Back, f.ImageFront, f.ImageBack, f.DeckID, f.Modified, f.ID)
	return err
}

func (s *SqliteSRS) DeleteFact(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM facts WHERE id=?`, id)
	return err
}

//
// Cards.
//

func (s *SqliteSRS) AddCard(ctx context.Context, c *srs.Card) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO cards (id, fact_id, deck_id, card_view, grade, easiness,
			acq_reps, ret_reps, lapses, acq_reps_since_lapse, ret_reps_since_lapse,
			last_rep, next_rep, active)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.FactID, c.DeckID, string(c.View), c.Grade, c.Easiness,
		c.AcqReps, c.RetReps, c.Lapses, c.AcqRepsSinceLapse, c.RetRepsSinceLapse,
		c.LastRep, c.NextRep, boolToInt(c.Active))
	return err
}

func (s *SqliteSRS) GetCard(ctx context.Context, id string) (*srs.Card, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+cardColumns+` FROM cards WHERE id=?`, id)
	c, err := scanCard(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, srs.ErrNotFound
	}
	return c, err
}

func (s *SqliteSRS) UpdateCard(ctx context.Context, c *srs.Card) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE cards SET fact_id=?, deck_id=?, card_view=?, grade=?, easiness=?,
			acq_reps=?, ret_reps=?, lapses=?, acq_reps_since_lapse=?,
			ret_reps_since_lapse=?, last_rep=?, next_rep=?, active=?
		 WHERE id=?`,
		c.FactID, c.DeckID, string(c.View), c.Grade, c.Easiness,
		c.AcqReps, c.RetReps, c.Lapses, c.AcqRepsSinceLapse, c.RetRepsSinceLapse,
		c.LastRep, c.NextRep, boolToInt(c.Active), c.ID)
	return err
}

func (s *SqliteSRS) DeleteCard(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cards WHERE id=?`, id)
	return err
}

func (s *SqliteSRS) CardsForFact(ctx context.Context, factID string) ([]*srs.Card, error) {
	return s.queryCards(ctx, `fact_id=?`, "rowid", -1, nil, factID)
}

//
// Decks.
//

func (s *SqliteSRS) AddDeck(ctx context.Context, d *srs.Deck) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO decks (id, name) VALUES (?, ?)`, d.ID, d.Name)
	return err
}

func (s *SqliteSRS) GetDeck(ctx context.Context, id string) (*srs.Deck, error) {
	var d srs.Deck
	err := s.db.QueryRowContext(ctx, `SELECT id, name FROM decks WHERE id=?`, id).Scan(&d.ID, &d.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, srs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *SqliteSRS) ListDecks(ctx context.Context) ([]*srs.Deck, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM decks ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var decks []*srs.Deck
	for rows.Next() {
		var d srs.Deck
		if err := rows.Scan(&d.ID, &d.Name); err != nil {
			return nil, err
		}
		decks = append(decks, &d)
	}
	return decks, rows.Err()
}

func (s *SqliteSRS) DeleteDeck(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM facts WHERE deck_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM decks WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SqliteSRS) ListFactsByDeck(ctx context.Context, deckID string) ([]*srs.Fact, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, front, back, image_front, image_back, deck_id, created, modified FROM facts WHERE deck_id=? ORDER BY created DESC`, deckID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var facts []*srs.Fact
	for rows.Next() {
		var f srs.Fact
		if err := rows.Scan(&f.ID, &f.Front, &f.Back, &f.ImageFront, &f.ImageBack, &f.DeckID, &f.Created, &f.Modified); err != nil {
			return nil, err
		}
		facts = append(facts, &f)
	}
	return facts, rows.Err()
}

//
// Reviews.
//

func (s *SqliteSRS) AddReview(ctx context.Context, r *srs.Review) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO reviews (id, card_id, grade, scheduled_interval,
			actual_interval, thinking_time, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.CardID, r.Grade, r.ScheduledInterval, r.ActualInterval,
		r.ThinkingTime, r.Timestamp)
	return err
}

//
// Counts.
//

func (s *SqliteSRS) ActiveCount(ctx context.Context) (int, error) {
	return s.count(ctx, `SELECT count(*) FROM cards WHERE active=1`)
}

func (s *SqliteSRS) ScheduledCount(ctx context.Context, now int64, deckIDs []string) (int, error) {
	q, args := buildCountQuery(`SELECT count(*) FROM cards WHERE active=1 AND grade>=2 AND next_rep<=?`, []any{now}, deckIDs)
	return s.count(ctx, q, args...)
}

func (s *SqliteSRS) NonMemorisedCount(ctx context.Context, deckIDs []string) (int, error) {
	q, args := buildCountQuery(`SELECT count(*) FROM cards WHERE active=1 AND grade<2`, nil, deckIDs)
	return s.count(ctx, q, args...)
}

//
// Queue selection.
//

func (s *SqliteSRS) CardsDueForRetRep(ctx context.Context, now int64, sortKey string, limit int, deckIDs []string) ([]*srs.Card, error) {
	return s.queryCards(ctx, `active=1 AND grade>=2 AND ?>=next_rep`, sortClause(sortKey), limit, deckIDs, now)
}

func (s *SqliteSRS) CardsToRelearn(ctx context.Context, grade int, sortKey string, deckIDs []string) ([]*srs.Card, error) {
	return s.queryCards(ctx, `active=1 AND grade=? AND lapses>0`, sortClause(sortKey), -1, deckIDs, grade)
}

func (s *SqliteSRS) CardsNewMemorising(ctx context.Context, grade int, deckIDs []string) ([]*srs.Card, error) {
	return s.queryCards(ctx, `active=1 AND grade=? AND lapses=0`, sortClause(""), -1, deckIDs, grade)
}

func (s *SqliteSRS) CardsUnseen(ctx context.Context, sortKey string, limit int, deckIDs []string) ([]*srs.Card, error) {
	return s.queryCards(ctx, `active=1 AND grade=-1`, sortClause(sortKey), limit, deckIDs)
}

func (s *SqliteSRS) CardsLearnAhead(ctx context.Context, now int64, sortKey string, limit int, deckIDs []string) ([]*srs.Card, error) {
	return s.queryCards(ctx, `active=1 AND grade>=2 AND ?<next_rep`, sortClause(sortKey), limit, deckIDs, now)
}

func (s *SqliteSRS) SisterCardCountScheduledBetween(ctx context.Context, factID, excludeCardID string, from, to int64) (int, error) {
	return s.count(ctx,
		`SELECT count(*) FROM cards WHERE fact_id=? AND id<>? AND active=1
		 AND grade>=2 AND next_rep>=? AND next_rep<?`,
		factID, excludeCardID, from, to)
}

//
// Helpers.
//

func (s *SqliteSRS) count(ctx context.Context, query string, args ...any) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

// queryCards runs a card SELECT with the given WHERE clause, ORDER BY clause,
// limit (<=0 meaning unlimited), and optional deck filter. where is a constant
// string; only bound args are user-controlled.
func (s *SqliteSRS) queryCards(ctx context.Context, where, order string, limit int, deckIDs []string, args ...any) ([]*srs.Card, error) {
	if limit <= 0 {
		limit = -1 // SQLite: negative LIMIT means no limit.
	}
	if len(deckIDs) > 0 {
		clause, deckArgs := inClause(deckIDs)
		where += " AND deck_id IN " + clause
		args = append(args, deckArgs...)
	}
	query := `SELECT ` + cardColumns + ` FROM cards WHERE ` + where + ` ORDER BY ` + order + ` LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cards []*srs.Card
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		cards = append(cards, c)
	}
	return cards, rows.Err()
}

// inClause builds a SQL IN placeholder string and corresponding args slice.
func inClause(ids []string) (string, []any) {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	return "(" + strings.Join(placeholders, ",") + ")", args
}

// buildCountQuery appends an optional deck_id IN filter to a count query.
func buildCountQuery(base string, baseArgs []any, deckIDs []string) (string, []any) {
	args := append([]any{}, baseArgs...)
	if len(deckIDs) > 0 {
		clause, deckArgs := inClause(deckIDs)
		base += " AND deck_id IN " + clause
		args = append(args, deckArgs...)
	}
	return base, args
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanCard(sc rowScanner) (*srs.Card, error) {
	var c srs.Card
	var view string
	var active int
	err := sc.Scan(&c.ID, &c.FactID, &c.DeckID, &view, &c.Grade, &c.Easiness,
		&c.AcqReps, &c.RetReps, &c.Lapses, &c.AcqRepsSinceLapse, &c.RetRepsSinceLapse,
		&c.LastRep, &c.NextRep, &active)
	if err != nil {
		return nil, err
	}
	c.View = srs.CardView(view)
	c.Active = active != 0
	return &c, nil
}

// sortClause whitelists the sort keys the scheduler uses, mapping them to SQL.
// The result is concatenated into the query, so it must never be user input.
func sortClause(sortKey string) string {
	switch sortKey {
	case "random":
		return "random()"
	case "interval":
		return "next_rep - last_rep"
	case "-interval":
		return "last_rep - next_rep"
	case "next_rep":
		return "next_rep"
	default: // "" and anything unrecognised: insertion order.
		return "rowid"
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
