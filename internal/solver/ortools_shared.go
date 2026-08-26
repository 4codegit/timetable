//go:build ortools

package solver

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
	}
	return -1
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
