package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"

	_ "github.com/mattn/go-sqlite3"

	"timetable/internal/domain"
)

// Store wraps the database and provides CRUD for all entities.
type Store struct {
	DB *sql.DB
}

// New opens (or creates) the SQLite database and runs migrations.
func New(path string) (*Store, error) {
	d, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	s := &Store{DB: d}
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
		if _, err := s.DB.Exec(st); err != nil {
			return err
		}
	}
	return nil
}

// ---- Schools ----

func (s *Store) CreateSchool(name string) (*domain.School, error) {
	res, err := s.DB.Exec(`INSERT INTO schools (name, settings_json) VALUES (?, '{}')`, name)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &domain.School{ID: int(id), Name: name, SettingsJSON: "{}"}, nil
}

func (s *Store) ListSchools() ([]domain.School, error) {
	rows, err := s.DB.Query(`SELECT id, name, settings_json FROM schools`)
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

func (s *Store) GetSchoolSettings(id int) (string, error) {
	var js string
	err := s.DB.QueryRow(`SELECT settings_json FROM schools WHERE id = ?`, id).Scan(&js)
	return js, err
}

func (s *Store) UpdateSchoolSettings(id int, settings string) error {
	_, err := s.DB.Exec(`UPDATE schools SET settings_json = ? WHERE id = ?`, settings, id)
	return err
}

// ---- Teachers ----

func (s *Store) CreateTeacher(t domain.Teacher) (*domain.Teacher, error) {
	res, err := s.DB.Exec(`INSERT INTO teachers (school_id, name, short_name, max_hours_per_week, preferences_json) VALUES (?,?,?,?,?)`,
		t.SchoolID, t.Name, t.ShortName, t.MaxHoursPerWeek, orDefault(t.PreferencesJSON, "{}"))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	t.ID = int(id)
	return &t, nil
}

func (s *Store) ListTeachers(schoolID int) ([]domain.Teacher, error) {
	rows, err := s.DB.Query(`SELECT id, school_id, name, short_name, max_hours_per_week, preferences_json FROM teachers WHERE school_id=?`, schoolID)
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
	res, err := s.DB.Exec(`INSERT INTO subjects (school_id, name, short_name, requires_room_type) VALUES (?,?,?,?)`,
		sub.SchoolID, sub.Name, sub.ShortName, orDefault(sub.RequiresRoomType, "any"))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	sub.ID = int(id)
	return &sub, nil
}

func (s *Store) ListSubjects(schoolID int) ([]domain.Subject, error) {
	rows, err := s.DB.Query(`SELECT id, school_id, name, short_name, requires_room_type FROM subjects WHERE school_id=?`, schoolID)
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
	res, err := s.DB.Exec(`INSERT INTO classes (school_id, name, grade, student_count, subgroup_of) VALUES (?,?,?,?,?)`,
		c.SchoolID, c.Name, c.Grade, c.StudentCount, c.SubgroupOf)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	c.ID = int(id)
	return &c, nil
}

func (s *Store) ListClasses(schoolID int) ([]domain.SchoolClass, error) {
	rows, err := s.DB.Query(`SELECT id, school_id, name, grade, student_count, subgroup_of FROM classes WHERE school_id=?`, schoolID)
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

// ---- Rooms ----

func (s *Store) CreateRoom(r domain.Room) (*domain.Room, error) {
	res, err := s.DB.Exec(`INSERT INTO rooms (school_id, name, capacity, room_type) VALUES (?,?,?,?)`,
		r.SchoolID, r.Name, r.Capacity, orDefault(r.RoomType, "any"))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	r.ID = int(id)
	return &r, nil
}

func (s *Store) ListRooms(schoolID int) ([]domain.Room, error) {
	rows, err := s.DB.Query(`SELECT id, school_id, name, capacity, room_type FROM rooms WHERE school_id=?`, schoolID)
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
	res, err := s.DB.Exec(`INSERT INTO lessons (school_id, class_id, subject_id, teacher_id, hours_per_week, min_gap_days, can_split, preferred_rooms) VALUES (?,?,?,?,?,?,?,?)`,
		l.SchoolID, l.ClassID, l.SubjectID, l.TeacherID, l.HoursPerWeek, l.MinGapDays, l.CanSplit, orDefault(l.PreferredRooms, "[]"))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	l.ID = int(id)
	return &l, nil
}

func (s *Store) ListLessons(schoolID int) ([]domain.Lesson, error) {
	rows, err := s.DB.Query(`SELECT id, school_id, class_id, subject_id, teacher_id, hours_per_week, min_gap_days, can_split, preferred_rooms FROM lessons WHERE school_id=?`, schoolID)
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
	_, err := s.DB.Exec(`DELETE FROM lessons WHERE id=?`, id)
	return err
}

// ---- Constraints ----

func (s *Store) CreateConstraint(c domain.Constraint) (*domain.Constraint, error) {
	res, err := s.DB.Exec(`INSERT INTO constraints (school_id, type, entity_type, entity_id, day_of_week, timeslot_start, timeslot_end, weight, is_hard, params_json) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		c.SchoolID, c.Type, c.EntityType, c.EntityID, c.DayOfWeek, c.TimeslotStart, c.TimeslotEnd, c.Weight, c.IsHard, orDefault(c.ParamsJSON, "{}"))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	c.ID = int(id)
	return &c, nil
}

func (s *Store) ListConstraints(schoolID int) ([]domain.Constraint, error) {
	rows, err := s.DB.Query(`SELECT id, school_id, type, entity_type, entity_id, day_of_week, timeslot_start, timeslot_end, weight, is_hard, params_json FROM constraints WHERE school_id=?`, schoolID)
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
	_, err := s.DB.Exec(`DELETE FROM schedule_entries WHERE school_id=?`, schoolID)
	return err
}

// SaveSchedule bulk-inserts generated entries.
func (s *Store) SaveSchedule(entries []domain.ScheduleEntry) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := s.DB.Begin()
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
	rows, err := s.DB.Query(`SELECT id, school_id, lesson_id, class_id, teacher_id, subject_id, room_id, day_of_week, timeslot, week_type FROM schedule_entries WHERE school_id=?`, schoolID)
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
	_, err := s.DB.Exec(`UPDATE schedule_entries SET day_of_week=?, timeslot=? WHERE id=?`, day, slot, id)
	return err
}

// ReplaceSchedule clears and re-inserts the whole schedule (used after manual edits / re-solve).
func (s *Store) ReplaceSchedule(schoolID int, entries []domain.ScheduleEntry) error {
	if err := s.ClearSchedule(schoolID); err != nil {
		return err
	}
	return s.SaveSchedule(entries)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	// basic JSON validation
	if !json.Valid([]byte(v)) && (v == "[]" || v == "{}") {
		return def
	}
	return v
}

var ErrNotFound = errors.New("not found")

func init() {
	_ = log.Println
}
