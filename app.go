package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"timetable/internal/db"
	"timetable/internal/domain"
	"timetable/internal/io"
	"timetable/internal/solver"
)

// App is the Wails-bound backend.
type App struct {
	ctx  context.Context
	store *db.Store
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// Greet is a sanity endpoint.
func (a *App) Greet(name string) string {
	return "Hello " + name + ", welcome to Timetable!"
}

// ---- Schools ----

func (a *App) CreateSchool(name string) (*domain.School, error) {
	return a.store.CreateSchool(name)
}

func (a *App) ListSchools() ([]domain.School, error) {
	return a.store.ListSchools()
}

// ---- Teachers ----

func (a *App) CreateTeacher(t domain.Teacher) (*domain.Teacher, error) {
	return a.store.CreateTeacher(t)
}

func (a *App) ListTeachers(schoolID int) ([]domain.Teacher, error) {
	return a.store.ListTeachers(schoolID)
}

// ---- Subjects ----

func (a *App) CreateSubject(s domain.Subject) (*domain.Subject, error) {
	return a.store.CreateSubject(s)
}

func (a *App) ListSubjects(schoolID int) ([]domain.Subject, error) {
	return a.store.ListSubjects(schoolID)
}

// ---- Classes ----

func (a *App) CreateClass(c domain.SchoolClass) (*domain.SchoolClass, error) {
	return a.store.CreateClass(c)
}

func (a *App) ListClasses(schoolID int) ([]domain.SchoolClass, error) {
	return a.store.ListClasses(schoolID)
}

// ---- Rooms ----

func (a *App) CreateRoom(r domain.Room) (*domain.Room, error) {
	return a.store.CreateRoom(r)
}

func (a *App) ListRooms(schoolID int) ([]domain.Room, error) {
	return a.store.ListRooms(schoolID)
}

// ---- Lessons ----

func (a *App) CreateLesson(l domain.Lesson) (*domain.Lesson, error) {
	return a.store.CreateLesson(l)
}

func (a *App) ListLessons(schoolID int) ([]domain.Lesson, error) {
	return a.store.ListLessons(schoolID)
}

func (a *App) DeleteLesson(id int) error {
	return a.store.DeleteLesson(id)
}

// ---- Constraints ----

func (a *App) CreateConstraint(c domain.Constraint) (*domain.Constraint, error) {
	return a.store.CreateConstraint(c)
}

func (a *App) ListConstraints(schoolID int) ([]domain.Constraint, error) {
	return a.store.ListConstraints(schoolID)
}

// ---- Scheduling ----

// Generate runs the CSP solver and persists the result.
func (a *App) Generate(schoolID, days, slots int) (*solver.Result, error) {
	lessons, err := a.store.ListLessons(schoolID)
	if err != nil {
		return nil, err
	}
	ts, _ := a.store.ListTeachers(schoolID)
	cs, _ := a.store.ListClasses(schoolID)
	rs, _ := a.store.ListRooms(schoolID)
	subs, _ := a.store.ListSubjects(schoolID)
	cons, _ := a.store.ListConstraints(schoolID)

	teacherMap := toTeacherMap(ts)
	classMap := toClassMap(cs)
	subjMap := toSubjMap(subs)

	in := solver.SolveInput{
		SchoolID:   schoolID,
		Lessons:    lessons,
		Teachers:   teacherMap,
		Classes:    classMap,
		Rooms:      rs,
		Subjects:   subjMap,
		Constraints: cons,
		Config:     domain.SchedulingConfig{DaysPerWeek: days, SlotsPerDay: slots},
	}

	res := solver.Solve(a.ctx, in, runtime.NumCPU(), 30*time.Second)

	if err := a.store.ClearSchedule(schoolID); err != nil {
		return nil, err
	}
	if err := a.store.SaveSchedule(res.Entries); err != nil {
		return nil, err
	}
	return &res, nil
}

// ListSchedule returns stored entries.
func (a *App) ListSchedule(schoolID int) ([]domain.ScheduleEntry, error) {
	return a.store.ListSchedule(schoolID)
}

// MoveEntry relocates a schedule entry after a manual drag-and-drop edit.
func (a *App) MoveEntry(id, day, slot int) error {
	return a.store.MoveEntry(id, day, slot)
}

// ReplaceSchedule overwrites the whole schedule (used by undo).
func (a *App) ReplaceSchedule(schoolID int, entries []domain.ScheduleEntry) error {
	return a.store.ReplaceSchedule(schoolID, entries)
}

// GeneratePrecise prefers the OR-Tools CP-SAT solver (when compiled with -tags ortools),
// otherwise falls back to the pure-Go backtracking solver.
func (a *App) GeneratePrecise(schoolID, days, slots int) (*solver.Result, error) {
	lessons, err := a.store.ListLessons(schoolID)
	if err != nil {
		return nil, err
	}
	ts, _ := a.store.ListTeachers(schoolID)
	cs, _ := a.store.ListClasses(schoolID)
	rs, _ := a.store.ListRooms(schoolID)
	subs, _ := a.store.ListSubjects(schoolID)
	cons, _ := a.store.ListConstraints(schoolID)

	in := solver.SolveInput{
		SchoolID:    schoolID,
		Lessons:     lessons,
		Teachers:    toTeacherMap(ts),
		Classes:     toClassMap(cs),
		Rooms:       rs,
		Subjects:    toSubjMap(subs),
		Constraints: cons,
		Config:      domain.SchedulingConfig{DaysPerWeek: days, SlotsPerDay: slots},
	}

	res := solver.SolvePrecise(a.ctx, in, runtime.NumCPU(), 60*time.Second)
	if err := a.store.ReplaceSchedule(schoolID, res.Entries); err != nil {
		return nil, err
	}
	return &res, nil
}

// ---- Import / Export ----

// ExportRefsCSV returns a CSV representation of a reference entity
// (teachers | classes | subjects | rooms | lessons) for the given school.
func (a *App) ExportRefsCSV(schoolID int, entity string) (string, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	switch entity {
	case "teachers":
		ts, err := a.store.ListTeachers(schoolID)
		if err != nil {
			return "", err
		}
		w.Write([]string{"name", "short_name", "max_hours_per_week"})
		for _, t := range ts {
			w.Write([]string{t.Name, t.ShortName, strconv.Itoa(t.MaxHoursPerWeek)})
		}
	case "classes":
		cs, err := a.store.ListClasses(schoolID)
		if err != nil {
			return "", err
		}
		w.Write([]string{"name", "grade", "student_count", "subgroup_of"})
		for _, c := range cs {
			sub := ""
			if c.SubgroupOf != nil {
				sub = strconv.Itoa(*c.SubgroupOf)
			}
			w.Write([]string{c.Name, strconv.Itoa(c.Grade), strconv.Itoa(c.StudentCount), sub})
		}
	case "subjects":
		ss, err := a.store.ListSubjects(schoolID)
		if err != nil {
			return "", err
		}
		w.Write([]string{"name", "short_name", "requires_room_type"})
		for _, s := range ss {
			w.Write([]string{s.Name, s.ShortName, s.RequiresRoomType})
		}
	case "rooms":
		rs, err := a.store.ListRooms(schoolID)
		if err != nil {
			return "", err
		}
		w.Write([]string{"name", "capacity", "room_type"})
		for _, r := range rs {
			w.Write([]string{r.Name, strconv.Itoa(r.Capacity), r.RoomType})
		}
	case "lessons":
		ls, err := a.store.ListLessons(schoolID)
		if err != nil {
			return "", err
		}
		cs, _ := a.store.ListClasses(schoolID)
		ss, _ := a.store.ListSubjects(schoolID)
		ts, _ := a.store.ListTeachers(schoolID)
		cMap := map[int]string{}
		for _, c := range cs {
			cMap[c.ID] = c.Name
		}
		sMap := map[int]string{}
		for _, s := range ss {
			sMap[s.ID] = s.Name
		}
		tMap := map[int]string{}
		for _, t := range ts {
			tMap[t.ID] = t.Name
		}
		w.Write([]string{"class", "subject", "teacher", "hours_per_week", "min_gap_days"})
		for _, l := range ls {
			w.Write([]string{cMap[l.ClassID], sMap[l.SubjectID], tMap[l.TeacherID], strconv.Itoa(l.HoursPerWeek), strconv.Itoa(l.MinGapDays)})
		}
	case "periods":
		st := a.loadSettings(schoolID)
		w.Write([]string{"period", "start", "end"})
		for i, p := range st.Periods {
			w.Write([]string{strconv.Itoa(i + 1), p.Start, p.End})
		}
	default:
		return "", fmt.Errorf("unknown entity %q", entity)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ImportRefsCSV parses a CSV file (with header) for the given entity and inserts
// rows into the database. For "lessons" the class/subject/teacher are resolved by name.
// Returns the number of inserted rows.
func (a *App) ImportRefsCSV(schoolID int, entity string, csvText string) (int, error) {
	r := csv.NewReader(strings.NewReader(csvText))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return 0, fmt.Errorf("csv parse: %w", err)
	}
	if len(records) < 2 {
		return 0, fmt.Errorf("no data rows")
	}
	// skip header if first field looks like a header
	start := 0
	if records[0][0] == "name" || records[0][0] == "class" {
		start = 1
	}

	count := 0
	switch entity {
	case "teachers":
		for _, row := range records[start:] {
			if len(row) < 1 || strings.TrimSpace(row[0]) == "" {
				continue
			}
			short := ""
			if len(row) > 1 {
				short = row[1]
			}
			maxh := 30
			if len(row) > 2 {
				if v, err := strconv.Atoi(strings.TrimSpace(row[2])); err == nil {
					maxh = v
				}
			}
			if _, err := a.store.CreateTeacher(domain.Teacher{SchoolID: schoolID, Name: row[0], ShortName: short, MaxHoursPerWeek: maxh}); err != nil {
				return count, err
			}
			count++
		}
	case "classes":
		for _, row := range records[start:] {
			if len(row) < 1 || strings.TrimSpace(row[0]) == "" {
				continue
			}
			grade := 0
			if len(row) > 1 {
				if v, err := strconv.Atoi(strings.TrimSpace(row[1])); err == nil {
					grade = v
				}
			}
			stu := 0
			if len(row) > 2 {
				if v, err := strconv.Atoi(strings.TrimSpace(row[2])); err == nil {
					stu = v
				}
			}
			var sub *int
			if len(row) > 3 && strings.TrimSpace(row[3]) != "" {
				if v, err := strconv.Atoi(strings.TrimSpace(row[3])); err == nil {
					sub = &v
				}
			}
			if _, err := a.store.CreateClass(domain.SchoolClass{SchoolID: schoolID, Name: row[0], Grade: grade, StudentCount: stu, SubgroupOf: sub}); err != nil {
				return count, err
			}
			count++
		}
	case "subjects":
		for _, row := range records[start:] {
			if len(row) < 1 || strings.TrimSpace(row[0]) == "" {
				continue
			}
			short := ""
			if len(row) > 1 {
				short = row[1]
			}
			rt := "any"
			if len(row) > 2 && strings.TrimSpace(row[2]) != "" {
				rt = row[2]
			}
			if _, err := a.store.CreateSubject(domain.Subject{SchoolID: schoolID, Name: row[0], ShortName: short, RequiresRoomType: rt}); err != nil {
				return count, err
			}
			count++
		}
	case "rooms":
		for _, row := range records[start:] {
			if len(row) < 1 || strings.TrimSpace(row[0]) == "" {
				continue
			}
			cap := 30
			if len(row) > 1 {
				if v, err := strconv.Atoi(strings.TrimSpace(row[1])); err == nil {
					cap = v
				}
			}
			rt := "any"
			if len(row) > 2 && strings.TrimSpace(row[2]) != "" {
				rt = row[2]
			}
			if _, err := a.store.CreateRoom(domain.Room{SchoolID: schoolID, Name: row[0], Capacity: cap, RoomType: rt}); err != nil {
				return count, err
			}
			count++
		}
	case "lessons":
		cs, _ := a.store.ListClasses(schoolID)
		ss, _ := a.store.ListSubjects(schoolID)
		ts, _ := a.store.ListTeachers(schoolID)
		cID := map[string]int{}
		for _, c := range cs {
			cID[c.Name] = c.ID
		}
		sID := map[string]int{}
		for _, s := range ss {
			sID[s.Name] = s.ID
		}
		tID := map[string]int{}
		for _, t := range ts {
			tID[t.Name] = t.ID
		}
		for _, row := range records[start:] {
			if len(row) < 3 || strings.TrimSpace(row[0]) == "" {
				continue
			}
			classID, ok := cID[row[0]]
			if !ok {
				return count, fmt.Errorf("class not found: %q", row[0])
			}
			subjID, ok := sID[row[1]]
			if !ok {
				return count, fmt.Errorf("subject not found: %q", row[1])
			}
			teachID, ok := tID[row[2]]
			if !ok {
				return count, fmt.Errorf("teacher not found: %q", row[2])
			}
			hours := 1
			if len(row) > 3 {
				if v, err := strconv.Atoi(strings.TrimSpace(row[3])); err == nil {
					hours = v
				}
			}
			gap := 1
			if len(row) > 4 {
				if v, err := strconv.Atoi(strings.TrimSpace(row[4])); err == nil {
					gap = v
				}
			}
			if _, err := a.store.CreateLesson(domain.Lesson{SchoolID: schoolID, ClassID: classID, SubjectID: subjID, TeacherID: teachID, HoursPerWeek: hours, MinGapDays: gap, CanSplit: false, PreferredRooms: "[]"}); err != nil {
				return count, err
			}
			count++
		}
	case "periods":
		st := a.loadSettings(schoolID)
		var ps []period
		for _, row := range records[start:] {
			if len(row) < 3 || strings.TrimSpace(row[0]) == "" {
				continue
			}
			ps = append(ps, period{Start: strings.TrimSpace(row[1]), End: strings.TrimSpace(row[2])})
		}
		st.Periods = ps
		st.Slots = len(ps)
		if err := a.saveSettings(schoolID, st); err != nil {
			return count, err
		}
		count = len(ps)
	default:
		return 0, fmt.Errorf("unknown entity %q", entity)
	}
	return count, nil
}

// ---- School settings (grid size + bell schedule) ----

type period struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type schoolSettings struct {
	Days    int      `json:"days"`
	Slots   int      `json:"slots"`
	Periods []period `json:"periods"`
}

func defaultPeriods(n int) []period {
	out := make([]period, n)
	for i := 0; i < n; i++ {
		startMin := 8*60 + i*45
		out[i] = period{
			Start: fmt.Sprintf("%02d:%02d", startMin/60, startMin%60),
			End:   fmt.Sprintf("%02d:%02d", (startMin+45)/60, (startMin+45)%60),
		}
	}
	return out
}

func (a *App) loadSettings(schoolID int) schoolSettings {
	raw, _ := a.store.GetSchoolSettings(schoolID)
	st := schoolSettings{Days: 6, Slots: 8}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &st)
	}
	if st.Days <= 0 {
		st.Days = 6
	}
	if st.Slots <= 0 {
		st.Slots = 8
	}
	if len(st.Periods) != st.Slots {
		st.Periods = defaultPeriods(st.Slots)
	}
	return st
}

func (a *App) saveSettings(schoolID int, st schoolSettings) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return a.store.UpdateSchoolSettings(schoolID, string(b))
}

// GetSchoolSettings returns the raw settings JSON for a school.
func (a *App) GetSchoolSettings(schoolID int) (string, error) {
	return a.store.GetSchoolSettings(schoolID)
}

// UpdateSchoolSettings persists the raw settings JSON for a school.
func (a *App) UpdateSchoolSettings(schoolID int, settings string) error {
	return a.store.UpdateSchoolSettings(schoolID, settings)
}

func (a *App) ExportAll(schoolID int) (*io.Snapshot, error) {
	return io.ExportAll(a.store, schoolID)
}

func (a *App) ImportAll(data string) error {
	snap, err := io.ParseSnapshot([]byte(data))
	if err != nil {
		return err
	}
	return io.ImportAll(a.store, snap)
}

func (a *App) ScheduleCSV(schoolID, days, slots int) (string, error) {
	entries, err := a.store.ListSchedule(schoolID)
	if err != nil {
		return "", err
	}
	ts, _ := a.store.ListTeachers(schoolID)
	cs, _ := a.store.ListClasses(schoolID)
	rs, _ := a.store.ListRooms(schoolID)
	subs, _ := a.store.ListSubjects(schoolID)
	return io.ScheduleCSV(entries, toClassMap(cs), toTeacherMap(ts), toSubjMap(subs), toRoomMap(rs), days, slots)
}

func toTeacherMap(ts []domain.Teacher) map[int]domain.Teacher {
	m := map[int]domain.Teacher{}
	for _, t := range ts {
		m[t.ID] = t
	}
	return m
}

func toClassMap(cs []domain.SchoolClass) map[int]domain.SchoolClass {
	m := map[int]domain.SchoolClass{}
	for _, c := range cs {
		m[c.ID] = c
	}
	return m
}

func toSubjMap(subs []domain.Subject) map[int]domain.Subject {
	m := map[int]domain.Subject{}
	for _, s := range subs {
		m[s.ID] = s
	}
	return m
}

func toRoomMap(rs []domain.Room) map[int]domain.Room {
	m := map[int]domain.Room{}
	for _, r := range rs {
		m[r.ID] = r
	}
	return m
}
