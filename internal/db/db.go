package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	_ "github.com/mattn/go-sqlite3"

	"timetable/internal/domain"
)

// dbtx is the interface used by Store for queries. Both *sql.DB and *sql.Tx
// satisfy it, so WithTx can transparently wrap a transaction.
type dbtx interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Prepare(query string) (*sql.Stmt, error)
}

// Store wraps the database and provides CRUD for all entities.
// db is used for all queries (works with both *sql.DB and *sql.Tx).
// root is the underlying *sql.DB used only for Begin/Close.
type Store struct {
	db   dbtx
	root *sql.DB
}

// New opens (or creates) the SQLite database and runs migrations.
func New(path string) (*Store, error) {
	d, err := sql.Open("sqlite3", path+"?_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	s := &Store{db: d, root: d}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schools (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			settings_json TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE TABLE IF NOT EXISTS teachers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			school_id INTEGER REFERENCES schools(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			short_name TEXT,
			max_hours_per_week INTEGER DEFAULT 30,
			preferences_json TEXT DEFAULT '{}'
		)`,
		`CREATE TABLE IF NOT EXISTS subjects (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			school_id INTEGER REFERENCES schools(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			short_name TEXT,
			requires_room_type TEXT DEFAULT 'any'
		)`,
		`CREATE TABLE IF NOT EXISTS classes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			school_id INTEGER REFERENCES schools(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			grade INTEGER DEFAULT 0,
			student_count INTEGER DEFAULT 0,
			subgroup_of INTEGER REFERENCES classes(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS rooms (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			school_id INTEGER REFERENCES schools(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			capacity INTEGER DEFAULT 30,
			room_type TEXT DEFAULT 'any'
		)`,
		`CREATE TABLE IF NOT EXISTS lessons (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			school_id INTEGER REFERENCES schools(id) ON DELETE CASCADE,
			class_id INTEGER REFERENCES classes(id) ON DELETE CASCADE,
			subject_id INTEGER REFERENCES subjects(id) ON DELETE CASCADE,
			teacher_id INTEGER REFERENCES teachers(id) ON DELETE CASCADE,
			hours_per_week INTEGER NOT NULL DEFAULT 1,
			min_gap_days INTEGER DEFAULT 1,
			can_split BOOLEAN DEFAULT FALSE,
			preferred_rooms TEXT DEFAULT '[]'
		)`,
		`CREATE TABLE IF NOT EXISTS constraints (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			school_id INTEGER REFERENCES schools(id) ON DELETE CASCADE,
			type TEXT NOT NULL,
			entity_type TEXT NOT NULL,
			entity_id INTEGER NOT NULL,
			day_of_week INTEGER,
			timeslot_start INTEGER,
			timeslot_end INTEGER,
			weight INTEGER DEFAULT 100,
			is_hard BOOLEAN DEFAULT TRUE,
			params_json TEXT DEFAULT '{}'
		)`,
		`CREATE TABLE IF NOT EXISTS schedule_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			school_id INTEGER REFERENCES schools(id) ON DELETE CASCADE,
			lesson_id INTEGER REFERENCES lessons(id) ON DELETE CASCADE,
			class_id INTEGER REFERENCES classes(id) ON DELETE CASCADE,
			teacher_id INTEGER REFERENCES teachers(id) ON DELETE CASCADE,
			subject_id INTEGER REFERENCES subjects(id) ON DELETE CASCADE,
			room_id INTEGER REFERENCES rooms(id) ON DELETE CASCADE,
			day_of_week INTEGER NOT NULL,
			timeslot INTEGER NOT NULL,
			week_type INTEGER DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_se_lookup ON schedule_entries(school_id, day_of_week, timeslot)`,
	}
	for _, st := range stmts {
		if _, err := s.db.Exec(st); err != nil {
			return err
		}
	}
	return nil
}

// ---- Schools ----

func (s *Store) CreateSchool(name string) (*domain.School, error) {
	res, err := s.db.Exec(`INSERT INTO schools (name, settings_json) VALUES (?, '{}')`, name)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &domain.School{ID: int(id), Name: name, SettingsJSON: "{}"}, nil
}

func (s *Store) ListSchools() ([]domain.School, error) {
	rows, err := s.db.Query(`SELECT id, name, settings_json FROM schools`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.School
	for rows.Next() {
		var sc domain.School
		if err := rows.Scan(&sc.ID, &sc.Name, &sc.SettingsJSON); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, nil
}

// GetSchool returns one school or sql.ErrNoRows when it does not exist.
func (s *Store) GetSchool(id int) (*domain.School, error) {
	var sc domain.School
	err := s.db.QueryRow(`SELECT id, name, settings_json FROM schools WHERE id=?`, id).Scan(&sc.ID, &sc.Name, &sc.SettingsJSON)
	if err != nil {
		return nil, err
	}
	return &sc, nil
}

// UpdateSchool restores the user-facing school metadata during an import.
func (s *Store) UpdateSchool(sc domain.School) error {
	res, err := s.db.Exec(`UPDATE schools SET name=?, settings_json=? WHERE id=?`, sc.Name, orDefault(sc.SettingsJSON, "{}"), sc.ID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("school %d not found", sc.ID)
	}
	return nil
}

func (s *Store) GetSchoolSettings(id int) (string, error) {
	var js string
	err := s.db.QueryRow(`SELECT settings_json FROM schools WHERE id = ?`, id).Scan(&js)
	return js, err
}

func (s *Store) UpdateSchoolSettings(id int, settings string) error {
	_, err := s.db.Exec(`UPDATE schools SET settings_json = ? WHERE id = ?`, settings, id)
	return err
}

// ---- Teachers ----

func (s *Store) CreateTeacher(t domain.Teacher) (*domain.Teacher, error) {
	res, err := s.db.Exec(`INSERT INTO teachers (school_id, name, short_name, max_hours_per_week, preferences_json) VALUES (?,?,?,?,?)`,
		t.SchoolID, t.Name, t.ShortName, t.MaxHoursPerWeek, orDefault(t.PreferencesJSON, "{}"))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	t.ID = int(id)
	return &t, nil
}

func (s *Store) ListTeachers(schoolID int) ([]domain.Teacher, error) {
	rows, err := s.db.Query(`SELECT id, school_id, name, short_name, max_hours_per_week, preferences_json FROM teachers WHERE school_id=?`, schoolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Teacher
	for rows.Next() {
		var t domain.Teacher
		if err := rows.Scan(&t.ID, &t.SchoolID, &t.Name, &t.ShortName, &t.MaxHoursPerWeek, &t.PreferencesJSON); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// ---- Subjects ----

func (s *Store) CreateSubject(sub domain.Subject) (*domain.Subject, error) {
	res, err := s.db.Exec(`INSERT INTO subjects (school_id, name, short_name, requires_room_type) VALUES (?,?,?,?)`,
		sub.SchoolID, sub.Name, sub.ShortName, orDefault(sub.RequiresRoomType, "any"))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	sub.ID = int(id)
	return &sub, nil
}

func (s *Store) ListSubjects(schoolID int) ([]domain.Subject, error) {
	rows, err := s.db.Query(`SELECT id, school_id, name, short_name, requires_room_type FROM subjects WHERE school_id=?`, schoolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Subject
	for rows.Next() {
		var sub domain.Subject
		if err := rows.Scan(&sub.ID, &sub.SchoolID, &sub.Name, &sub.ShortName, &sub.RequiresRoomType); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, nil
}

// ---- Classes ----

func (s *Store) CreateClass(c domain.SchoolClass) (*domain.SchoolClass, error) {
	res, err := s.db.Exec(`INSERT INTO classes (school_id, name, grade, student_count, subgroup_of) VALUES (?,?,?,?,?)`,
		c.SchoolID, c.Name, c.Grade, c.StudentCount, c.SubgroupOf)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	c.ID = int(id)
	return &c, nil
}

func (s *Store) ListClasses(schoolID int) ([]domain.SchoolClass, error) {
	rows, err := s.db.Query(`SELECT id, school_id, name, grade, student_count, subgroup_of FROM classes WHERE school_id=?`, schoolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SchoolClass
	for rows.Next() {
		var c domain.SchoolClass
		var sub sql.NullInt32
		if err := rows.Scan(&c.ID, &c.SchoolID, &c.Name, &c.Grade, &c.StudentCount, &sub); err != nil {
			return nil, err
		}
		if sub.Valid {
			v := int(sub.Int32)
			c.SubgroupOf = &v
		}
		out = append(out, c)
	}
	return out, nil
}

// UpdateClassSubgroup restores a class' optional parent after all imported
// classes have received their new IDs.
func (s *Store) UpdateClassSubgroup(id int, parent *int) error {
	_, err := s.db.Exec(`UPDATE classes SET subgroup_of=? WHERE id=?`, parent, id)
	return err
}

// ---- Rooms ----

func (s *Store) CreateRoom(r domain.Room) (*domain.Room, error) {
	res, err := s.db.Exec(`INSERT INTO rooms (school_id, name, capacity, room_type) VALUES (?,?,?,?)`,
		r.SchoolID, r.Name, r.Capacity, orDefault(r.RoomType, "any"))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	r.ID = int(id)
	return &r, nil
}

func (s *Store) ListRooms(schoolID int) ([]domain.Room, error) {
	rows, err := s.db.Query(`SELECT id, school_id, name, capacity, room_type FROM rooms WHERE school_id=?`, schoolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Room
	for rows.Next() {
		var r domain.Room
		if err := rows.Scan(&r.ID, &r.SchoolID, &r.Name, &r.Capacity, &r.RoomType); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// ---- Lessons ----

func (s *Store) CreateLesson(l domain.Lesson) (*domain.Lesson, error) {
	res, err := s.db.Exec(`INSERT INTO lessons (school_id, class_id, subject_id, teacher_id, hours_per_week, min_gap_days, can_split, preferred_rooms) VALUES (?,?,?,?,?,?,?,?)`,
		l.SchoolID, l.ClassID, l.SubjectID, l.TeacherID, l.HoursPerWeek, l.MinGapDays, l.CanSplit, orDefault(l.PreferredRooms, "[]"))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	l.ID = int(id)
	return &l, nil
}

func (s *Store) ListLessons(schoolID int) ([]domain.Lesson, error) {
	rows, err := s.db.Query(`SELECT id, school_id, class_id, subject_id, teacher_id, hours_per_week, min_gap_days, can_split, preferred_rooms FROM lessons WHERE school_id=?`, schoolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Lesson
	for rows.Next() {
		var l domain.Lesson
		var canSplit bool
		if err := rows.Scan(&l.ID, &l.SchoolID, &l.ClassID, &l.SubjectID, &l.TeacherID, &l.HoursPerWeek, &l.MinGapDays, &canSplit, &l.PreferredRooms); err != nil {
			return nil, err
		}
		l.CanSplit = canSplit
		out = append(out, l)
	}
	return out, nil
}

func (s *Store) DeleteLesson(id int) error {
	_, err := s.db.Exec(`DELETE FROM lessons WHERE id=?`, id)
	return err
}

func (s *Store) UpdateLesson(l domain.Lesson) error {
	_, err := s.db.Exec(`UPDATE lessons SET class_id=?, subject_id=?, teacher_id=?, hours_per_week=?, min_gap_days=?, can_split=?, preferred_rooms=? WHERE id=?`,
		l.ClassID, l.SubjectID, l.TeacherID, l.HoursPerWeek, l.MinGapDays, l.CanSplit, orDefault(l.PreferredRooms, "[]"), l.ID)
	return err
}

func (s *Store) DeleteTeacher(id int) error {
	_, err := s.db.Exec(`DELETE FROM teachers WHERE id=?`, id)
	return err
}

func (s *Store) DeleteSubject(id int) error {
	_, err := s.db.Exec(`DELETE FROM subjects WHERE id=?`, id)
	return err
}

func (s *Store) DeleteClass(id int) error {
	_, err := s.db.Exec(`DELETE FROM classes WHERE id=?`, id)
	return err
}

func (s *Store) DeleteRoom(id int) error {
	_, err := s.db.Exec(`DELETE FROM rooms WHERE id=?`, id)
	return err
}

func (s *Store) DeleteConstraint(id int) error {
	_, err := s.db.Exec(`DELETE FROM constraints WHERE id=?`, id)
	return err
}

func (s *Store) DeleteScheduleEntry(id int) error {
	_, err := s.db.Exec(`DELETE FROM schedule_entries WHERE id=?`, id)
	return err
}

// ---- Constraints ----

func (s *Store) CreateConstraint(c domain.Constraint) (*domain.Constraint, error) {
	res, err := s.db.Exec(`INSERT INTO constraints (school_id, type, entity_type, entity_id, day_of_week, timeslot_start, timeslot_end, weight, is_hard, params_json) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		c.SchoolID, c.Type, c.EntityType, c.EntityID, c.DayOfWeek, c.TimeslotStart, c.TimeslotEnd, c.Weight, c.IsHard, orDefault(c.ParamsJSON, "{}"))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	c.ID = int(id)
	return &c, nil
}

func (s *Store) ListConstraints(schoolID int) ([]domain.Constraint, error) {
	rows, err := s.db.Query(`SELECT id, school_id, type, entity_type, entity_id, day_of_week, timeslot_start, timeslot_end, weight, is_hard, params_json FROM constraints WHERE school_id=?`, schoolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Constraint
	for rows.Next() {
		var c domain.Constraint
		var dow, ts, te sql.NullInt32
		var hard sql.NullBool
		if err := rows.Scan(&c.ID, &c.SchoolID, &c.Type, &c.EntityType, &c.EntityID, &dow, &ts, &te, &c.Weight, &hard, &c.ParamsJSON); err != nil {
			return nil, err
		}
		c.IsHard = hard.Bool
		if dow.Valid {
			v := int(dow.Int32)
			c.DayOfWeek = &v
		}
		if ts.Valid {
			v := int(ts.Int32)
			c.TimeslotStart = &v
		}
		if te.Valid {
			v := int(te.Int32)
			c.TimeslotEnd = &v
		}
		out = append(out, c)
	}
	return out, nil
}

// ---- Schedule entries ----

// ClearSchedule removes all entries for a school.
func (s *Store) ClearSchedule(schoolID int) error {
	_, err := s.db.Exec(`DELETE FROM schedule_entries WHERE school_id=?`, schoolID)
	return err
}

// SaveSchedule bulk-inserts generated entries.
func (s *Store) SaveSchedule(entries []domain.ScheduleEntry) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := s.root.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO schedule_entries (school_id, lesson_id, class_id, teacher_id, subject_id, room_id, day_of_week, timeslot, week_type) VALUES (?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, e := range entries {
		if _, err := stmt.Exec(e.SchoolID, e.LessonID, e.ClassID, e.TeacherID, e.SubjectID, e.RoomID, e.DayOfWeek, e.Timeslot, e.WeekType); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListSchedule(schoolID int) ([]domain.ScheduleEntry, error) {
	rows, err := s.db.Query(`SELECT id, school_id, lesson_id, class_id, teacher_id, subject_id, room_id, day_of_week, timeslot, week_type FROM schedule_entries WHERE school_id=?`, schoolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ScheduleEntry
	for rows.Next() {
		var e domain.ScheduleEntry
		if err := rows.Scan(&e.ID, &e.SchoolID, &e.LessonID, &e.ClassID, &e.TeacherID, &e.SubjectID, &e.RoomID, &e.DayOfWeek, &e.Timeslot, &e.WeekType); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// MoveEntry relocates a single schedule entry to a new day/slot (manual DnD edit).
func (s *Store) MoveEntry(id, day, slot int) error {
	res, err := s.db.Exec(`UPDATE schedule_entries SET day_of_week=?, timeslot=? WHERE id=?`, day, slot, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("schedule entry %d not found", id)
	}
	return nil
}

// SwapEntries atomically swaps two schedule entries' day/slot in one transaction.
// Semantics: after this call, entry id1 is at (day1, slot1) and entry id2 is at
// (day2, slot2). This matches the frontend's mental model of "src moves to the
// target cell, target moves to the source cell".
func (s *Store) SwapEntries(id1, day1, slot1, id2, day2, slot2 int) error {
	if id1 == id2 {
		return errors.New("cannot swap a schedule entry with itself")
	}
	return s.WithTx(func(tx *Store) error {
		res, err := tx.db.Exec(`UPDATE schedule_entries SET day_of_week=?, timeslot=? WHERE id=?`, day1, slot1, id1)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("schedule entry %d not found", id1)
		}
		res, err = tx.db.Exec(`UPDATE schedule_entries SET day_of_week=?, timeslot=? WHERE id=?`, day2, slot2, id2)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("schedule entry %d not found", id2)
		}
		return nil
	})
}

// ReplaceSchedule clears and re-inserts the whole schedule inside a single
// transaction so a failure never leaves an empty (half-cleared) schedule.
func (s *Store) ReplaceSchedule(schoolID int, entries []domain.ScheduleEntry) error {
	// ImportAll already owns a transaction. Starting another SQLite transaction
	// there causes "database is locked", so reuse the current transaction when
	// Store was created by WithTx.
	if _, inTransaction := s.db.(*sql.Tx); inTransaction {
		return s.replaceSchedule(schoolID, entries)
	}
	return s.WithTx(func(tx *Store) error {
		return tx.replaceSchedule(schoolID, entries)
	})
}

func (s *Store) replaceSchedule(schoolID int, entries []domain.ScheduleEntry) error {
	if _, err := s.db.Exec(`DELETE FROM schedule_entries WHERE school_id=?`, schoolID); err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	stmt, err := s.db.Prepare(`INSERT INTO schedule_entries (school_id, lesson_id, class_id, teacher_id, subject_id, room_id, day_of_week, timeslot, week_type) VALUES (?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range entries {
		if _, err := stmt.Exec(e.SchoolID, e.LessonID, e.ClassID, e.TeacherID, e.SubjectID, e.RoomID, e.DayOfWeek, e.Timeslot, e.WeekType); err != nil {
			return err
		}
	}
	return nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	if !json.Valid([]byte(v)) {
		return def
	}
	return v
}

// WithTx runs fn inside a database transaction. If fn returns an error the
// transaction is rolled back; otherwise it is committed. The Store passed to
// fn shares the same underlying DB so all existing methods work transparently.
func (s *Store) WithTx(fn func(*Store) error) error {
	tx, err := s.root.Begin()
	if err != nil {
		return err
	}
	txStore := &Store{db: tx, root: s.root}
	if err := fn(txStore); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ClearSchoolData removes all school-related data (schedule, lessons,
// constraints, teachers, subjects, classes, rooms) for the given school.
func (s *Store) ClearSchoolData(schoolID int) error {
	tables := []string{
		"schedule_entries",
		"lessons",
		"constraints",
		"rooms",
		"classes",
		"subjects",
		"teachers",
	}
	for _, table := range tables {
		if _, err := s.db.Exec(fmt.Sprintf("DELETE FROM %s WHERE school_id = ?", table), schoolID); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	return nil
}

// DeleteSchool removes a school and all its data (cascading).
func (s *Store) DeleteSchool(id int) error {
	_, err := s.db.Exec(`DELETE FROM schools WHERE id = ?`, id)
	return err
}

// SchoolHasLessons checks whether a school has any lessons defined.
// Used to warn before deletion.
func (s *Store) SchoolHasLessons(schoolID int) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM lessons WHERE school_id = ?`, schoolID).Scan(&count)
	return count > 0, err
}

// SchoolHasSchedule checks whether a school has generated schedule entries.
func (s *Store) SchoolHasSchedule(schoolID int) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM schedule_entries WHERE school_id = ?`, schoolID).Scan(&count)
	return count > 0, err
}

var ErrNotFound = errors.New("not found")

func init() {
	_ = log.Println
}
