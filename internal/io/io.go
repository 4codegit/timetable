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

// ImportAll inserts a snapshot into the DB inside a single transaction.
// If the school already exists (by ID > 0), all existing data for that school
// is cleared first to prevent duplicates. Old IDs are remapped to new ones
// so that cross-references (SubgroupOf, Lesson.ClassID, etc.) remain valid.
func ImportAll(s *db.Store, snap *Snapshot) error {
        return s.WithTx(func(tx *db.Store) error {
                var schoolID int
                if snap.School.ID == 0 {
                        sc, err := tx.CreateSchool(snap.School.Name)
                        if err != nil {
                                return fmt.Errorf("create school: %w", err)
                        }
                        schoolID = sc.ID
                        // Restore settings from snapshot
                        if snap.School.SettingsJSON != "" {
                                _ = tx.UpdateSchoolSettings(schoolID, snap.School.SettingsJSON)
                        }
                } else {
                        schoolID = snap.School.ID
                        // Clear existing school data to prevent duplicates
                        if err := tx.ClearSchoolData(schoolID); err != nil {
                                return fmt.Errorf("clear school data: %w", err)
                        }
                }

                // ID remapping: old snapshot ID -> new DB ID.
                teacherIDMap := map[int]int{}
                subjectIDMap := map[int]int{}
                classIDMap := map[int]int{}
                roomIDMap := map[int]int{}

                for _, t := range snap.Teachers {
                        oldID := t.ID
                        t.SchoolID = schoolID
                        t.ID = 0
                        t.PreferencesJSON = orEmptyJSON(t.PreferencesJSON)
                        created, err := tx.CreateTeacher(t)
                        if err != nil {
                                return fmt.Errorf("create teacher %q: %w", t.Name, err)
                        }
                        teacherIDMap[oldID] = created.ID
                }

                for _, sub := range snap.Subjects {
                        oldID := sub.ID
                        sub.SchoolID = schoolID
                        sub.ID = 0
                        sub.RequiresRoomType = orDefaultStr(sub.RequiresRoomType, "any")
                        created, err := tx.CreateSubject(sub)
                        if err != nil {
                                return fmt.Errorf("create subject %q: %w", sub.Name, err)
                        }
                        subjectIDMap[oldID] = created.ID
                }

                for _, c := range snap.Classes {
                        oldID := c.ID
                        c.SchoolID = schoolID
                        c.ID = 0
                        if c.SubgroupOf != nil {
                                if newID, ok := classIDMap[*c.SubgroupOf]; ok {
                                        c.SubgroupOf = &newID
                                } else {
                                        c.SubgroupOf = nil
                                }
                        }
                        created, err := tx.CreateClass(c)
                        if err != nil {
                                return fmt.Errorf("create class %q: %w", c.Name, err)
                        }
                        classIDMap[oldID] = created.ID
                }

                for _, r := range snap.Rooms {
                        oldID := r.ID
                        r.SchoolID = schoolID
                        r.ID = 0
                        r.RoomType = orDefaultStr(r.RoomType, "any")
                        created, err := tx.CreateRoom(r)
                        if err != nil {
                                return fmt.Errorf("create room %q: %w", r.Name, err)
                        }
                        roomIDMap[oldID] = created.ID
                }

                for _, l := range snap.Lessons {
                        l.SchoolID = schoolID
                        l.ID = 0
                        if newID, ok := classIDMap[l.ClassID]; ok {
                                l.ClassID = newID
                        }
                        if newID, ok := subjectIDMap[l.SubjectID]; ok {
                                l.SubjectID = newID
                        }
                        if newID, ok := teacherIDMap[l.TeacherID]; ok {
                                l.TeacherID = newID
                        }
                        l.PreferredRooms = orEmptyJSON(l.PreferredRooms)
                        if _, err := tx.CreateLesson(l); err != nil {
                                return fmt.Errorf("create lesson: %w", err)
                        }
                }

                for _, c := range snap.Constraints {
                        c.SchoolID = schoolID
                        c.ID = 0
                        c.ParamsJSON = orEmptyJSON(c.ParamsJSON)
                        if _, err := tx.CreateConstraint(c); err != nil {
                                return fmt.Errorf("create constraint: %w", err)
                        }
                }

                return nil
        })
}

// orEmptyJSON returns the value if non-empty, otherwise "{}".
func orEmptyJSON(v string) string {
        if v == "" {
                return "{}"
        }
        return v
}

// orDefaultStr returns v if non-empty, otherwise def.
func orDefaultStr(v, def string) string {
        if v == "" {
                return def
        }
        return v
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
                subj := "?"
                if s, ok := subjects[e.SubjectID]; ok {
                        subj = s.Name
                }
                teach := "?"
                if t, ok := teachers[e.TeacherID]; ok {
                        if t.ShortName != "" {
                                teach = t.ShortName
                        } else {
                                teach = t.Name
                        }
                }
                room := "?"
                if r, ok := rooms[e.RoomID]; ok {
                        room = r.Name
                }
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
