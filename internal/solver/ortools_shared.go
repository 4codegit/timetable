//go:build ortools

package solver

import (
	"timetable/internal/domain"
)

func constraintTypeCode(t string) int {
	switch t {
	case "teacher_unavailable":
		return 0
	case "class_unavailable":
		return 1
	case "room_unavailable":
		return 2
	case "max_consecutive":
		return 3
	case "lunch_break":
		return 4
	case "max_lessons_per_day":
		return 5
	case "min_lessons_per_day":
		return 6
	case "prefer_morning":
		return 7
	case "max_gaps":
		return 8
	case "student_group":
		return 9
	}
	return -1
}

// appendStudentGroupConstraints добавляет синтетические жёсткие
// ограничения student_group: подгруппа (entity_id) + родитель (value).
// TimeslotStart несёт предмет подгруппы — биндинг запрещает только пары
// РАЗНЫХ предметов (делёный предмет: обе половинки параллельно).
func appendStudentGroupConstraints(in SolveInput) SolveInput {
	groups := map[int][]int{}
	for _, c := range in.Classes {
		if c.SubgroupOf != nil {
			if _, ok := in.Classes[*c.SubgroupOf]; ok {
				groups[*c.SubgroupOf] = append(groups[*c.SubgroupOf], c.ID)
			}
		}
	}
	if len(groups) == 0 {
		return in
	}
	out := in
	out.Constraints = append([]domain.Constraint(nil), in.Constraints...)
	for parent, kids := range groups {
		for _, kid := range kids {
			subj := subSubjectOf(kid, in.Lessons)
			out.Constraints = append(out.Constraints, domain.Constraint{
				Type:          "student_group",
				EntityType:    "class",
				EntityID:      kid,
				Weight:        parent,
				TimeslotStart: &subj,
				IsHard:        true,
			})
		}
	}
	return out
}

// subSubjectOf: предмет первого урока подгруппы (0 если нет).
func subSubjectOf(childClass int, lessons []domain.Lesson) int {
	for _, l := range lessons {
		if l.ClassID == childClass {
			return l.SubjectID
		}
	}
	return 0
}

func entityTypeCode(t string) int {
	switch t {
	case "teacher":
		return 0
	case "class":
		return 1
	case "room":
		return 2
	case "school":
		return 3
	}
	return -1
}
