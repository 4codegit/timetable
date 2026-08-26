package io

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"

	"timetable/internal/db"
	"timetable/internal/domain"
)

// Snapshot is the full exportable state of a school.
type Snapshot struct {
	School      domain.School      `json:"school"`
	Teachers    []domain.Teacher   `json:"teachers"`
	Subjects    []domain.Subject   `json:"subjects"`
	Classes     []domain.SchoolClass      `json:"classes"`
	Rooms       []domain.Room       `json:"rooms"`
	Lessons     []domain.Lesson     `json:"lessons"`
	Constraints []domain.Constraint `json:"constraints"`
}

// ExportAll gathers a school's data into a Snapshot.
func ExportAll(s *db.Store, schoolID int) (*Snapshot, error) {
	schools, err := s.ListSchools()
	if err != nil {
		return nil, err
	}
	var school domain.School
	found := false
	for _, sc := range schools {
		if sc.ID == schoolID {
			school = sc
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("school %d not found", schoolID)
	}
	t, _ := s.ListTeachers(schoolID)
	sub, _ := s.ListSubjects(schoolID)
	cl, _ := s.ListClasses(schoolID)
	r, _ := s.ListRooms(schoolID)
	l, _ := s.ListLessons(schoolID)
	c, _ := s.ListConstraints(schoolID)
	return &Snapshot{
		School: school, Teachers: t, Subjects: sub, Classes: cl,
		Rooms: r, Lessons: l, Constraints: c,
	}, nil
}

// ImportAll inserts a snapshot into the DB (teachers/subjects/classes/rooms first).
func ImportAll(s *db.Store, snap *Snapshot) error {
	if snap.School.ID == 0 {
		sc, err := s.CreateSchool(snap.School.Name)
		if err != nil {
			return err
		}
		snap.School.ID = sc.ID
	}
	for _, t := range snap.Teachers {
		t.SchoolID = snap.School.ID
		if _, err := s.CreateTeacher(t); err != nil {
			return err
		}
	}
	for _, sub := range snap.Subjects {
		sub.SchoolID = snap.School.ID
		if _, err := s.CreateSubject(sub); err != nil {
			return err
		}
	}
	for _, c := range snap.Classes {
		c.SchoolID = snap.School.ID
		if _, err := s.CreateClass(c); err != nil {
			return err
		}
	}
	for _, r := range snap.Rooms {
		r.SchoolID = snap.School.ID
		if _, err := s.CreateRoom(r); err != nil {
			return err
		}
	}
	for _, l := range snap.Lessons {
		l.SchoolID = snap.School.ID
		if _, err := s.CreateLesson(l); err != nil {
			return err
		}
	}
	for _, c := range snap.Constraints {
		c.SchoolID = snap.School.ID
		if _, err := s.CreateConstraint(c); err != nil {
			return err
		}
	}
	return nil
}

// ScheduleCSV renders entries as a week×timeslot CSV keyed by class.
func ScheduleCSV(entries []domain.ScheduleEntry, classes map[int]domain.SchoolClass, teachers map[int]domain.Teacher, subjects map[int]domain.Subject, rooms map[int]domain.Room, days, slots int) (string, error) {
	var b strings.Builder
	w := csv.NewWriter(&b)

	header := []string{"День/Слот"}
	for s := 0; s < slots; s++ {
		header = append(header, fmt.Sprintf("Пара %d", s+1))
	}
	if err := w.Write(header); err != nil {
		return "", err
	}

	dayNames := []string{"Пн", "Вт", "Ср", "Чт", "Пт", "Сб", "Вс"}
	// grid[classID][day][slot] = label
	grid := map[int]map[int]map[int]string{}
	for _, e := range entries {
		if grid[e.ClassID] == nil {
			grid[e.ClassID] = map[int]map[int]string{}
		}
		if grid[e.ClassID][e.DayOfWeek] == nil {
			grid[e.ClassID][e.DayOfWeek] = map[int]string{}
		}
		subj := subjects[e.SubjectID].Name
		teach := teachers[e.TeacherID].ShortName
		if teach == "" {
			teach = teachers[e.TeacherID].Name
		}
		room := rooms[e.RoomID].Name
		grid[e.ClassID][e.DayOfWeek][e.Timeslot] = fmt.Sprintf("%s (%s) %s", subj, teach, room)
	}

	for cid, c := range classes {
		for d := 0; d < days; d++ {
			row := []string{c.Name + " " + dayName(dayNames, d)}
			for s := 0; s < slots; s++ {
				row = append(row, grid[cid][d][s])
			}
			if err := w.Write(row); err != nil {
				return "", err
			}
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", err
	}
	return b.String(), nil
}

func dayName(names []string, d int) string {
	if d >= 0 && d < len(names) {
		return names[d]
	}
	return fmt.Sprintf("Д%d", d+1)
}

// ParseSnapshot decodes a JSON snapshot.
func ParseSnapshot(data []byte) (*Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
