// Package database provides the SQLite-backed implementation of the srs
// domain's Repository interface and the auth feature's Store.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/contre95/stella/src/srs"
	_ "modernc.org/sqlite"
)

// SqliteSRS implements srs.Repository on top of SQLite.
type SqliteSRS struct {
	db *sql.DB
}

// Compile-time assertion that we satisfy the domain contract.
var _ srs.Repository = (*SqliteSRS)(nil)

// cardColumns is the fixed column list used by every card SELECT, matching the
// order scanCard expects.
const cardColumns = `id, user_id, sync_id, fact_id, deck_id, card_view, grade, easiness,
	acq_reps, ret_reps, lapses, acq_reps_since_lapse, ret_reps_since_lapse,
	last_rep, next_rep, active`

// NewSqliteSRS opens (or creates) the database at path and ensures the schema.
func NewSqliteSRS(path string) (*SqliteSRS, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	// A single connection avoids SQLITE_BUSY on concurrent writes.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("connecting to database: %w", err)
	}
	s := &SqliteSRS{db: db}
	if err := s.createTables(); err != nil {
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *SqliteSRS) Close() error { return s.db.Close() }

func (s *SqliteSRS) createTables() error {
	const schema = `
CREATE TABLE IF NOT EXISTS users (
	id      TEXT PRIMARY KEY,
	issuer  TEXT NOT NULL,
	subject TEXT NOT NULL,
	email   TEXT NOT NULL DEFAULT '',
	name    TEXT NOT NULL DEFAULT '',
	created INTEGER NOT NULL,
	UNIQUE (issuer, subject)
);

CREATE TABLE IF NOT EXISTS sessions (
	token_hash TEXT PRIMARY KEY,
	user_id    TEXT NOT NULL,
	expires    INTEGER NOT NULL,
	FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS api_tokens (
	token_hash TEXT PRIMARY KEY,
	user_id    TEXT NOT NULL,
	name       TEXT NOT NULL,
	created    INTEGER NOT NULL,
	FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS groups (
	id      TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	name    TEXT NOT NULL,
	FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS decks (
	id            TEXT PRIMARY KEY,
	user_id       TEXT NOT NULL,
	sync_id       TEXT NOT NULL DEFAULT '',
	name          TEXT NOT NULL,
	color         TEXT NOT NULL DEFAULT '#8b5cf6',
	description   TEXT NOT NULL DEFAULT '',
	image         TEXT NOT NULL DEFAULT '',
	group_id      TEXT,
	study_enabled INTEGER NOT NULL DEFAULT 1,
	FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS tags (
	id      TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	name    TEXT NOT NULL,
	FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
	UNIQUE (user_id, name)
);

CREATE TABLE IF NOT EXISTS deck_tags (
	deck_id TEXT NOT NULL,
	tag_id  TEXT NOT NULL,
	PRIMARY KEY (deck_id, tag_id),
	FOREIGN KEY (deck_id) REFERENCES decks(id) ON DELETE CASCADE,
	FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS facts (
	id          TEXT PRIMARY KEY,
	user_id     TEXT NOT NULL,
	front       TEXT NOT NULL,
	back        TEXT NOT NULL,
	image_front TEXT NOT NULL DEFAULT '',
	image_back  TEXT NOT NULL DEFAULT '',
	audio_front TEXT NOT NULL DEFAULT '',
	audio_back  TEXT NOT NULL DEFAULT '',
	deck_id     TEXT NOT NULL,
	created     INTEGER NOT NULL,
	modified    INTEGER NOT NULL,
	FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
	FOREIGN KEY (deck_id) REFERENCES decks(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_facts_user_created ON facts(user_id, created);

CREATE TABLE IF NOT EXISTS cards (
	id                   TEXT PRIMARY KEY,
	user_id              TEXT NOT NULL,
	sync_id              TEXT NOT NULL DEFAULT '',
	fact_id              TEXT NOT NULL,
	deck_id              TEXT NOT NULL,
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
	FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
	FOREIGN KEY (fact_id) REFERENCES facts(id) ON DELETE CASCADE,
	FOREIGN KEY (deck_id) REFERENCES decks(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_cards_due  ON cards(user_id, active, grade, next_rep);
CREATE INDEX IF NOT EXISTS idx_cards_fact ON cards(fact_id);

CREATE TABLE IF NOT EXISTS reviews (
	id                 TEXT PRIMARY KEY,
	user_id            TEXT NOT NULL,
	card_id            TEXT NOT NULL,
	grade              INTEGER NOT NULL,
	ret_reps           INTEGER NOT NULL DEFAULT 0,
	scheduled_interval INTEGER NOT NULL,
	actual_interval    INTEGER NOT NULL,
	thinking_time      INTEGER NOT NULL,
	timestamp          INTEGER NOT NULL,
	source             TEXT NOT NULL DEFAULT 'web',
	FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_reviews_card ON reviews(card_id);
CREATE INDEX IF NOT EXISTS idx_reviews_ts   ON reviews(user_id, timestamp);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("creating schema: %w", err)
	}
	// Per-user uniqueness for the sync IDs assigned to every deck and card on
	// insert (AddDeck/AddCard retry on the astronomically rare collision).
	const indexes = `
CREATE UNIQUE INDEX IF NOT EXISTS idx_decks_sync ON decks(user_id, sync_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_cards_sync ON cards(user_id, sync_id);
`
	if _, err := s.db.Exec(indexes); err != nil {
		return fmt.Errorf("creating sync indexes: %w", err)
	}
	return nil
}

// isSyncIDCollision reports whether err is a violation of one of the per-user
// sync_id unique indexes, so inserts can retry with a fresh ID.
func isSyncIDCollision(err error) bool {
	return err != nil && strings.Contains(err.Error(), ".sync_id")
}

//
// Facts.
//

func (s *SqliteSRS) AddFact(ctx context.Context, f *srs.Fact) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO facts (id, user_id, front, back, image_front, image_back, audio_front, audio_back, deck_id, created, modified)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.UserID, f.Front, f.Back, f.ImageFront, f.ImageBack, f.AudioFront, f.AudioBack, f.DeckID, f.Created, f.Modified)
	return err
}

func (s *SqliteSRS) GetFact(ctx context.Context, userID, id string) (*srs.Fact, error) {
	var f srs.Fact
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, front, back, image_front, image_back, audio_front, audio_back, deck_id, created, modified
		 FROM facts WHERE id=? AND user_id=?`, id, userID).
		Scan(&f.ID, &f.UserID, &f.Front, &f.Back, &f.ImageFront, &f.ImageBack, &f.AudioFront, &f.AudioBack, &f.DeckID, &f.Created, &f.Modified)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, srs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// ListFacts returns a page of the user's facts, newest first, optionally
// restricted to those whose front or back contain search (case-insensitive).
// A limit<=0 means unlimited.
func (s *SqliteSRS) ListFacts(ctx context.Context, userID, search string, limit, offset int) ([]*srs.Fact, error) {
	where, args := factSearchClause(userID, search)
	if limit <= 0 {
		limit = -1 // SQLite: negative LIMIT means no limit.
	}
	args = append(args, limit, offset)
	return s.queryFacts(ctx,
		`SELECT id, user_id, front, back, image_front, image_back, audio_front, audio_back, deck_id, created, modified
		 FROM facts WHERE `+where+` ORDER BY created DESC LIMIT ? OFFSET ?`, args...)
}

// CountFacts returns how many of the user's facts match ListFacts' same
// search filter, for computing page counts.
func (s *SqliteSRS) CountFacts(ctx context.Context, userID, search string) (int, error) {
	where, args := factSearchClause(userID, search)
	return s.count(ctx, `SELECT count(*) FROM facts WHERE `+where, args...)
}

// factSearchClause builds the WHERE clause and bound args shared by ListFacts
// and CountFacts: user scoping plus an optional case-insensitive front/back
// substring match. LIKE metacharacters in search are escaped so a literal
// "%" or "_" typed by the user is matched literally.
func factSearchClause(userID, search string) (string, []any) {
	if search == "" {
		return "user_id=?", []any{userID}
	}
	pattern := "%" + escapeLike(search) + "%"
	return `user_id=? AND (front LIKE ? ESCAPE '\' OR back LIKE ? ESCAPE '\')`, []any{userID, pattern, pattern}
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func (s *SqliteSRS) UpdateFact(ctx context.Context, f *srs.Fact) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE facts SET front=?, back=?, image_front=?, image_back=?, audio_front=?, audio_back=?, deck_id=?, modified=?
		 WHERE id=? AND user_id=?`,
		f.Front, f.Back, f.ImageFront, f.ImageBack, f.AudioFront, f.AudioBack, f.DeckID, f.Modified, f.ID, f.UserID)
	return err
}

func (s *SqliteSRS) DeleteFact(ctx context.Context, userID, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM facts WHERE id=? AND user_id=?`, id, userID)
	return err
}

func (s *SqliteSRS) ListFactsByDeck(ctx context.Context, userID, deckID string) ([]*srs.Fact, error) {
	return s.queryFacts(ctx,
		`SELECT id, user_id, front, back, image_front, image_back, audio_front, audio_back, deck_id, created, modified
		 FROM facts WHERE deck_id=? AND user_id=? ORDER BY created DESC`, deckID, userID)
}

func (s *SqliteSRS) queryFacts(ctx context.Context, query string, args ...any) ([]*srs.Fact, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var facts []*srs.Fact
	for rows.Next() {
		var f srs.Fact
		if err := rows.Scan(&f.ID, &f.UserID, &f.Front, &f.Back, &f.ImageFront, &f.ImageBack, &f.AudioFront, &f.AudioBack, &f.DeckID, &f.Created, &f.Modified); err != nil {
			return nil, err
		}
		facts = append(facts, &f)
	}
	return facts, rows.Err()
}

//
// Cards.
//

// AddCard inserts a card, assigning it a fresh sync ID (retrying the
// astronomically rare per-user collision). c.SyncID is set on success.
func (s *SqliteSRS) AddCard(ctx context.Context, c *srs.Card) error {
	for {
		c.SyncID = srs.NewSyncID()
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO cards (id, user_id, sync_id, fact_id, deck_id, card_view, grade, easiness,
				acq_reps, ret_reps, lapses, acq_reps_since_lapse, ret_reps_since_lapse,
				last_rep, next_rep, active)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			c.ID, c.UserID, c.SyncID, c.FactID, c.DeckID, string(c.View), c.Grade, c.Easiness,
			c.AcqReps, c.RetReps, c.Lapses, c.AcqRepsSinceLapse, c.RetRepsSinceLapse,
			c.LastRep, c.NextRep, boolToInt(c.Active))
		if !isSyncIDCollision(err) {
			return err
		}
	}
}

func (s *SqliteSRS) GetCard(ctx context.Context, userID, id string) (*srs.Card, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+cardColumns+` FROM cards WHERE id=? AND user_id=?`, id, userID)
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
		 WHERE id=? AND user_id=?`,
		c.FactID, c.DeckID, string(c.View), c.Grade, c.Easiness,
		c.AcqReps, c.RetReps, c.Lapses, c.AcqRepsSinceLapse, c.RetRepsSinceLapse,
		c.LastRep, c.NextRep, boolToInt(c.Active), c.ID, c.UserID)
	return err
}

func (s *SqliteSRS) DeleteCard(ctx context.Context, userID, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cards WHERE id=? AND user_id=?`, id, userID)
	return err
}

func (s *SqliteSRS) CardsForFact(ctx context.Context, userID, factID string) ([]*srs.Card, error) {
	return s.queryCards(ctx, userID, `fact_id=?`, "rowid", -1, nil, factID)
}

//
// Device sync.
//

func (s *SqliteSRS) CardsByDeck(ctx context.Context, userID, deckID string) ([]*srs.Card, error) {
	return s.queryCards(ctx, userID, `deck_id=?`, "rowid", -1, nil, deckID)
}

func (s *SqliteSRS) DeckExportRows(ctx context.Context, userID, deckID string, limit int) ([]srs.DeckExportRow, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT c.sync_id, c.card_view, f.front, f.back
		 FROM cards c JOIN facts f ON c.fact_id = f.id
		 WHERE c.user_id=? AND c.deck_id=? AND c.active=1
		 ORDER BY c.rowid LIMIT ?`, userID, deckID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []srs.DeckExportRow
	for rows.Next() {
		var r srs.DeckExportRow
		var view string
		if err := rows.Scan(&r.SyncID, &view, &r.Front, &r.Back); err != nil {
			return nil, err
		}
		if srs.CardView(view) == srs.ViewReverse {
			r.Front, r.Back = r.Back, r.Front
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *SqliteSRS) CardIDsBySyncID(ctx context.Context, userID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sync_id, id FROM cards WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var syncID, id string
		if err := rows.Scan(&syncID, &id); err != nil {
			return nil, err
		}
		out[syncID] = id
	}
	return out, rows.Err()
}

//
// Decks.
//

// AddDeck inserts a deck, assigning it a fresh sync ID (retrying the
// astronomically rare per-user collision). d.SyncID is set on success.
func (s *SqliteSRS) AddDeck(ctx context.Context, d *srs.Deck) error {
	groupID := nullableString(d.GroupID)
	for {
		d.SyncID = srs.NewSyncID()
		_, err := s.db.ExecContext(ctx, `INSERT INTO decks (id, user_id, sync_id, name, color, description, image, group_id, study_enabled) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			d.ID, d.UserID, d.SyncID, d.Name, d.Color, d.Description, d.Image, groupID, boolToInt(d.StudyEnabled))
		if !isSyncIDCollision(err) {
			return err
		}
	}
}

func (s *SqliteSRS) GetDeck(ctx context.Context, userID, id string) (*srs.Deck, error) {
	return s.getDeckWhere(ctx, `id=? AND user_id=?`, id, userID)
}

func (s *SqliteSRS) GetDeckBySyncID(ctx context.Context, userID, syncID string) (*srs.Deck, error) {
	return s.getDeckWhere(ctx, `sync_id=? AND user_id=?`, syncID, userID)
}

func (s *SqliteSRS) getDeckWhere(ctx context.Context, where string, args ...any) (*srs.Deck, error) {
	var d srs.Deck
	var groupID sql.NullString
	var studyEnabled int
	err := s.db.QueryRowContext(ctx, `SELECT id, user_id, sync_id, name, color, description, image, group_id, study_enabled FROM decks WHERE `+where, args...).
		Scan(&d.ID, &d.UserID, &d.SyncID, &d.Name, &d.Color, &d.Description, &d.Image, &groupID, &studyEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, srs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if groupID.Valid {
		d.GroupID = groupID.String
	}
	d.StudyEnabled = studyEnabled != 0
	return &d, nil
}

func (s *SqliteSRS) UpdateDeck(ctx context.Context, d *srs.Deck) error {
	groupID := nullableString(d.GroupID)
	_, err := s.db.ExecContext(ctx, `UPDATE decks SET name=?, color=?, description=?, image=?, group_id=?, study_enabled=? WHERE id=? AND user_id=?`,
		d.Name, d.Color, d.Description, d.Image, groupID, boolToInt(d.StudyEnabled), d.ID, d.UserID)
	return err
}

func (s *SqliteSRS) ListDecks(ctx context.Context, userID string) ([]*srs.Deck, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, sync_id, name, color, description, image, group_id, study_enabled FROM decks WHERE user_id=? ORDER BY group_id IS NULL, group_id, name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var decks []*srs.Deck
	for rows.Next() {
		var d srs.Deck
		var groupID sql.NullString
		var studyEnabled int
		if err := rows.Scan(&d.ID, &d.UserID, &d.SyncID, &d.Name, &d.Color, &d.Description, &d.Image, &groupID, &studyEnabled); err != nil {
			return nil, err
		}
		if groupID.Valid {
			d.GroupID = groupID.String
		}
		d.StudyEnabled = studyEnabled != 0
		decks = append(decks, &d)
	}
	return decks, rows.Err()
}

func (s *SqliteSRS) ListStudyEnabledDeckIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM decks WHERE user_id=? AND study_enabled=1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *SqliteSRS) DeleteDeck(ctx context.Context, userID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM facts WHERE deck_id=? AND user_id=?`, id, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM decks WHERE id=? AND user_id=?`, id, userID); err != nil {
		return err
	}
	return tx.Commit()
}

//
// Groups.
//

func (s *SqliteSRS) AddGroup(ctx context.Context, g *srs.Group) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO groups (id, user_id, name) VALUES (?, ?, ?)`, g.ID, g.UserID, g.Name)
	return err
}

func (s *SqliteSRS) GetGroup(ctx context.Context, userID, id string) (*srs.Group, error) {
	var g srs.Group
	err := s.db.QueryRowContext(ctx, `SELECT id, user_id, name FROM groups WHERE id=? AND user_id=?`, id, userID).
		Scan(&g.ID, &g.UserID, &g.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, srs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (s *SqliteSRS) UpdateGroup(ctx context.Context, g *srs.Group) error {
	_, err := s.db.ExecContext(ctx, `UPDATE groups SET name=? WHERE id=? AND user_id=?`, g.Name, g.ID, g.UserID)
	return err
}

func (s *SqliteSRS) ListGroups(ctx context.Context, userID string) ([]*srs.Group, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, name FROM groups WHERE user_id=? ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []*srs.Group
	for rows.Next() {
		var g srs.Group
		if err := rows.Scan(&g.ID, &g.UserID, &g.Name); err != nil {
			return nil, err
		}
		groups = append(groups, &g)
	}
	return groups, rows.Err()
}

func (s *SqliteSRS) DeleteGroup(ctx context.Context, userID, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM groups WHERE id=? AND user_id=?`, id, userID)
	return err
}

func (s *SqliteSRS) UnsetGroupForDecks(ctx context.Context, userID, groupID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE decks SET group_id=NULL WHERE group_id=? AND user_id=?`, groupID, userID)
	return err
}

//
// Tags.
//

func (s *SqliteSRS) AddTag(ctx context.Context, t *srs.Tag) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO tags (id, user_id, name) VALUES (?, ?, ?)`, t.ID, t.UserID, t.Name)
	return err
}

func (s *SqliteSRS) GetTag(ctx context.Context, userID, id string) (*srs.Tag, error) {
	return s.getTagWhere(ctx, `id=? AND user_id=?`, id, userID)
}

func (s *SqliteSRS) GetTagByName(ctx context.Context, userID, name string) (*srs.Tag, error) {
	return s.getTagWhere(ctx, `name=? AND user_id=?`, name, userID)
}

func (s *SqliteSRS) getTagWhere(ctx context.Context, where string, args ...any) (*srs.Tag, error) {
	var t srs.Tag
	err := s.db.QueryRowContext(ctx, `SELECT id, user_id, name FROM tags WHERE `+where, args...).
		Scan(&t.ID, &t.UserID, &t.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, srs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *SqliteSRS) UpdateTag(ctx context.Context, t *srs.Tag) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tags SET name=? WHERE id=? AND user_id=?`, t.Name, t.ID, t.UserID)
	return err
}

func (s *SqliteSRS) ListTags(ctx context.Context, userID string) ([]*srs.Tag, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, name FROM tags WHERE user_id=? ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTags(rows)
}

func (s *SqliteSRS) DeleteTag(ctx context.Context, userID, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM tags WHERE id=? AND user_id=?`, id, userID)
	return err
}

// TagDeck links a deck to a tag. The join row is only inserted when both rows
// belong to userID, and re-linking an already-tagged deck is a no-op.
func (s *SqliteSRS) TagDeck(ctx context.Context, userID, deckID, tagID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO deck_tags (deck_id, tag_id)
		 SELECT d.id, t.id FROM decks d JOIN tags t ON t.user_id = d.user_id
		 WHERE d.id=? AND t.id=? AND d.user_id=?`, deckID, tagID, userID)
	return err
}

func (s *SqliteSRS) UntagDeck(ctx context.Context, userID, deckID, tagID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM deck_tags WHERE deck_id=? AND tag_id IN (SELECT id FROM tags WHERE id=? AND user_id=?)`,
		deckID, tagID, userID)
	return err
}

func (s *SqliteSRS) ListTagsForDeck(ctx context.Context, userID, deckID string) ([]*srs.Tag, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.user_id, t.name FROM tags t
		 JOIN deck_tags dt ON dt.tag_id = t.id
		 WHERE dt.deck_id=? AND t.user_id=? ORDER BY t.name`, deckID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTags(rows)
}

func (s *SqliteSRS) DeckIDsByTag(ctx context.Context, userID, tagID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT dt.deck_id FROM deck_tags dt
		 JOIN decks d ON d.id = dt.deck_id
		 WHERE dt.tag_id=? AND d.user_id=? ORDER BY d.name`, tagID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *SqliteSRS) TagsByDeck(ctx context.Context, userID string) (map[string][]*srs.Tag, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT dt.deck_id, t.id, t.user_id, t.name FROM deck_tags dt
		 JOIN tags t ON t.id = dt.tag_id
		 WHERE t.user_id=? ORDER BY t.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]*srs.Tag{}
	for rows.Next() {
		var deckID string
		var t srs.Tag
		if err := rows.Scan(&deckID, &t.ID, &t.UserID, &t.Name); err != nil {
			return nil, err
		}
		out[deckID] = append(out[deckID], &t)
	}
	return out, rows.Err()
}

func scanTags(rows *sql.Rows) ([]*srs.Tag, error) {
	var tags []*srs.Tag
	for rows.Next() {
		var t srs.Tag
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name); err != nil {
			return nil, err
		}
		tags = append(tags, &t)
	}
	return tags, rows.Err()
}

//
// Reviews.
//

func (s *SqliteSRS) AddReview(ctx context.Context, r *srs.Review) error {
	_, err := s.db.ExecContext(ctx, insertReviewSQL, reviewArgs(r)...)
	return err
}

// ReviewsForCard returns a card's full review log ordered oldest first.
func (s *SqliteSRS) ReviewsForCard(ctx context.Context, userID, cardID string) ([]*srs.Review, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, card_id, grade, ret_reps, scheduled_interval,
			actual_interval, thinking_time, timestamp, source
		 FROM reviews WHERE user_id=? AND card_id=? ORDER BY timestamp, rowid`, userID, cardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*srs.Review
	for rows.Next() {
		var r srs.Review
		if err := rows.Scan(&r.ID, &r.UserID, &r.CardID, &r.Grade, &r.RetReps,
			&r.ScheduledInterval, &r.ActualInterval, &r.ThinkingTime, &r.Timestamp, &r.Source); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// AddReviewIfNew inserts a review unless its ID already exists, reporting
// whether a row was inserted. Deterministic IDs make device uploads
// idempotent across retries.
func (s *SqliteSRS) AddReviewIfNew(ctx context.Context, r *srs.Review) (bool, error) {
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE`+insertReviewSQL[len(`INSERT`):], reviewArgs(r)...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

const insertReviewSQL = `INSERT INTO reviews (id, user_id, card_id, grade, ret_reps, scheduled_interval,
		actual_interval, thinking_time, timestamp, source)
	 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func reviewArgs(r *srs.Review) []any {
	source := r.Source
	if source == "" {
		source = "web"
	}
	return []any{r.ID, r.UserID, r.CardID, r.Grade, r.RetReps, r.ScheduledInterval, r.ActualInterval,
		r.ThinkingTime, r.Timestamp, source}
}

// FactIDsLearnedToday unions Mnemosyne's fact_ids_newly_learned_today (a
// repetition with grade >= 2 while ret_reps was still 0) and
// fact_ids_forgotten_and_learned_today (a grade >= 2 repetition on a card that
// lapsed from the retention phase earlier in the same window).
func (s *SqliteSRS) FactIDsLearnedToday(ctx context.Context, userID string, from, to int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT c.fact_id FROM reviews r
		JOIN cards c ON r.card_id = c.id
		WHERE r.user_id = ?3 AND r.timestamp >= ?1 AND r.timestamp < ?2
		  AND r.grade >= 2 AND r.ret_reps = 0
		UNION
		SELECT DISTINCT c.fact_id FROM reviews r
		JOIN cards c ON r.card_id = c.id
		WHERE r.user_id = ?3 AND r.timestamp >= ?1 AND r.timestamp < ?2 AND r.grade >= 2
		  AND r.card_id IN (
			SELECT card_id FROM reviews
			WHERE user_id = ?3 AND timestamp >= ?1 AND timestamp < ?2
			  AND grade < 2 AND ret_reps > 0)`, from, to, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *SqliteSRS) ReviewTimestampsSince(ctx context.Context, userID string, from int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT timestamp FROM reviews WHERE user_id=? AND timestamp>=? ORDER BY timestamp`, userID, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var ts int64
		if err := rows.Scan(&ts); err != nil {
			return nil, err
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}

//
// Counts.
//

func (s *SqliteSRS) ActiveCount(ctx context.Context, userID string) (int, error) {
	return s.count(ctx, `SELECT count(DISTINCT fact_id) FROM cards WHERE user_id=? AND active=1`, userID)
}

func (s *SqliteSRS) ActiveCountForDecks(ctx context.Context, userID string, deckIDs []string) (int, error) {
	q, args := buildCountQuery(`SELECT count(DISTINCT fact_id) FROM cards WHERE user_id=? AND active=1`, []any{userID}, deckIDs)
	return s.count(ctx, q, args...)
}

func (s *SqliteSRS) FactCountForDecks(ctx context.Context, userID string, deckIDs []string) (int, error) {
	q, args := buildCountQuery(`SELECT count(*) FROM facts WHERE user_id=?`, []any{userID}, deckIDs)
	return s.count(ctx, q, args...)
}

func (s *SqliteSRS) ScheduledCount(ctx context.Context, userID string, now int64, deckIDs []string) (int, error) {
	q, args := buildCountQuery(`SELECT count(*) FROM cards WHERE user_id=? AND active=1 AND grade>=2 AND next_rep<=?`, []any{userID, now}, deckIDs)
	return s.count(ctx, q, args...)
}

func (s *SqliteSRS) NonMemorisedCount(ctx context.Context, userID string, deckIDs []string) (int, error) {
	q, args := buildCountQuery(`SELECT count(DISTINCT fact_id) FROM cards WHERE user_id=? AND active=1 AND grade<2`, []any{userID}, deckIDs)
	return s.count(ctx, q, args...)
}

func (s *SqliteSRS) UnseenCount(ctx context.Context, userID string, deckIDs []string) (int, error) {
	q, args := buildCountQuery(`SELECT count(DISTINCT fact_id) FROM cards WHERE user_id=? AND active=1 AND grade=-1`, []any{userID}, deckIDs)
	return s.count(ctx, q, args...)
}

//
// Queue selection.
//

func (s *SqliteSRS) CardsDueForRetRep(ctx context.Context, userID string, now int64, sortKey string, limit int, deckIDs []string) ([]*srs.Card, error) {
	return s.queryCards(ctx, userID, `active=1 AND grade>=2 AND ?>=next_rep`, sortClause(sortKey), limit, deckIDs, now)
}

func (s *SqliteSRS) CardsToRelearn(ctx context.Context, userID string, grade int, sortKey string, deckIDs []string) ([]*srs.Card, error) {
	return s.queryCards(ctx, userID, `active=1 AND grade=? AND lapses>0`, sortClause(sortKey), -1, deckIDs, grade)
}

func (s *SqliteSRS) CardsNewMemorising(ctx context.Context, userID string, grade int, deckIDs []string) ([]*srs.Card, error) {
	return s.queryCards(ctx, userID, `active=1 AND grade=? AND lapses=0`, sortClause(""), -1, deckIDs, grade)
}

func (s *SqliteSRS) CardsUnseen(ctx context.Context, userID string, sortKey string, limit int, deckIDs []string) ([]*srs.Card, error) {
	return s.queryCards(ctx, userID, `active=1 AND grade=-1`, sortClause(sortKey), limit, deckIDs)
}

func (s *SqliteSRS) CardsLearnAhead(ctx context.Context, userID string, now int64, sortKey string, limit int, deckIDs []string) ([]*srs.Card, error) {
	return s.queryCards(ctx, userID, `active=1 AND grade>=2 AND ?<next_rep`, sortClause(sortKey), limit, deckIDs, now)
}

func (s *SqliteSRS) SisterCardCountScheduledBetween(ctx context.Context, userID, factID, excludeCardID string, from, to int64) (int, error) {
	return s.count(ctx,
		`SELECT count(*) FROM cards WHERE user_id=? AND fact_id=? AND id<>? AND active=1
		 AND grade>=2 AND next_rep>=? AND next_rep<?`,
		userID, factID, excludeCardID, from, to)
}

//
// Helpers.
//

func (s *SqliteSRS) count(ctx context.Context, query string, args ...any) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

// queryCards runs a user-scoped card SELECT with the given WHERE clause,
// ORDER BY clause, limit (<=0 meaning unlimited), and optional deck filter.
// where is a constant string; only bound args are user-controlled.
func (s *SqliteSRS) queryCards(ctx context.Context, userID, where, order string, limit int, deckIDs []string, args ...any) ([]*srs.Card, error) {
	if limit <= 0 {
		limit = -1 // SQLite: negative LIMIT means no limit.
	}
	where = `user_id=? AND (` + where + `)`
	args = append([]any{userID}, args...)
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
	err := sc.Scan(&c.ID, &c.UserID, &c.SyncID, &c.FactID, &c.DeckID, &view, &c.Grade, &c.Easiness,
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

// nullableString converts an empty Go string to a SQL NULL, and a non-empty
// string to a valid sql.NullString, so optional FK columns round-trip cleanly.
func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
