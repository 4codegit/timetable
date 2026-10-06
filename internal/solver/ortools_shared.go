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
// ограничения student_group: подгруппа (entity_id) не может пересекаться
// по времени с уроком родительского класса (value). Две подгруппы одного
// родителя при этом МОГУТ идти параллельно с разными учителями.
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
			out.Constraints = append(out.Constraints, domain.Constraint{
				Type:       "student_group",
				EntityType: "class",
				EntityID:   kid,
				Weight:     parent,
				IsHard:     true,
			})
		}
	}
	return out
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
