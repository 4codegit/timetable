package solver

import (
	"context"
	"encoding/json"
	"math/rand"
	"sort"
	"sync"
	"time"

	"timetable/internal/domain"
)

// Occurrence is one required placement of a lesson.
type Occurrence struct {
	Lesson      domain.Lesson
	Index       int // which occurrence (0..hours-1)
	RoomChoices []int
}

// SolveInput bundles everything the solver needs.
type SolveInput struct {
	SchoolID    int
	Lessons     []domain.Lesson
	Teachers    map[int]domain.Teacher
	Classes     map[int]domain.SchoolClass
	Rooms       []domain.Room
	Subjects    map[int]domain.Subject
	Constraints []domain.Constraint
	Config      domain.SchedulingConfig
}

// Result holds the best schedule found and its metric.
type Result struct {
	Entries    []domain.ScheduleEntry `json:"entries"`
	Placed     int                    `json:"placed"`
	Total      int                    `json:"total"`
	Violations int                    `json:"violations"` // soft constraint violations
}

// Solve runs parallel randomised-restart CSP search.
func Solve(ctx context.Context, in SolveInput, parallelism int, timeout time.Duration) Result {
	if parallelism < 1 {
		parallelism = 1
	}
	days := in.Config.DaysPerWeek
	if days <= 0 {
		days = 6
	}
	slots := in.Config.SlotsPerDay
	if slots <= 0 {
		slots = 8
	}

	occ := buildOccurrences(in, days, slots)
	hard := buildHard(in, days, slots)

	var best Result
	best.Total = len(occ)
	var mu sync.Mutex
	update := func(r Result) {
		mu.Lock()
		defer mu.Unlock()
		if r.Placed > best.Placed || (r.Placed == best.Placed && r.Violations < best.Violations) {
			best = r
		}
	}

	workerCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var wg sync.WaitGroup
	for w := 0; w < parallelism; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			local := runRestarts(workerCtx, in, occ, hard, days, slots, rng)
			if local.Placed > 0 {
				update(local)
			}
		}(time.Now().UnixNano() + int64(w*7919))
	}
	wg.Wait()
	return best
}

// runRestarts performs multiple randomised backtracking attempts and keeps the best.
func runRestarts(ctx context.Context, in SolveInput, occ []Occurrence, hard HardSet, days, slots int, rng *rand.Rand) Result {
	best := Result{Total: len(occ)}
	deadline, _ := ctx.Deadline()
	for {
		if time.Now().After(deadline) {
			break
		}
		r := backtrack(in, occ, hard, days, slots, rng)
		if r.Placed > best.Placed || (r.Placed == best.Placed && r.Violations < best.Violations) {
			best = r
		}
		if r.Placed == len(occ) && r.Violations == 0 {
			break // perfect
		}
		if r.Placed == len(occ) {
			// good enough, keep searching a bit for fewer violations
			if time.Now().After(deadline.Add(-200 * time.Millisecond)) {
				break
			}
		}
	}
	return best
}

func backtrack(in SolveInput, occ []Occurrence, hard HardSet, days, slots int, rng *rand.Rand) Result {
	// Order occurrences: most constrained first (fewest room choices).
	order := make([]int, len(occ))
	for i := range occ {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return len(occ[order[a]].RoomChoices) < len(occ[order[b]].RoomChoices)
	})

	teacherBusy := map[int][][]bool{}
	classBusy := map[int][][]bool{}
	roomBusy := map[int][][]bool{}
	for t := range in.Teachers {
		teacherBusy[t] = newGrid(days, slots)
	}
	for c := range in.Classes {
		classBusy[c] = newGrid(days, slots)
	}
	for _, r := range in.Rooms {
		roomBusy[r.ID] = newGrid(days, slots)
	}

	ss := buildStudentSets(in.Classes)

	teacherMaxCons, classMaxCons := map[int]int{}, map[int]int{}
	teacherMaxDay, classMaxDay := map[int]int{}, map[int]int{}
	for _, c := range in.Constraints {
		if !c.IsHard {
			continue
		}
		switch c.Type {
		case "max_consecutive":
			if c.EntityType == "teacher" {
				teacherMaxCons[c.EntityID] = c.Weight
			}
			if c.EntityType == "class" {
				classMaxCons[c.EntityID] = c.Weight
			}
		case "max_lessons_per_day":
			if c.EntityType == "teacher" {
				teacherMaxDay[c.EntityID] = c.Weight
			}
			if c.EntityType == "class" {
				classMaxDay[c.EntityID] = c.Weight
			}
		}
	}

	teacherDay := map[int][]int{}
	classDay := map[int][]int{}
	for t := range in.Teachers {
		teacherDay[t] = make([]int, days)
	}
	for c := range in.Classes {
		classDay[c] = make([]int, days)
	}

	assign := make([]cell, len(occ))
	var solution []cell
	ok := false

	var rec func(k int) bool
	rec = func(k int) bool {
		if k == len(order) {
			entries := buildEntries(in, assign, occ)
			if !validateAggregates(in, ss, entries, days, slots) {
				return false
			}
			sol := make([]cell, len(assign))
			copy(sol, assign)
			solution = sol
			ok = true
			return true
		}
		oi := order[k]
		o := occ[oi]
		cset := ss[o.Lesson.ClassID]
		cands := candidateCells(o, hard, days, slots, classBusy, cset, roomBusy)
		shuffle(cands, rng)
		tID := o.Lesson.TeacherID
		cID := o.Lesson.ClassID
		tMaxCons := teacherMaxCons[tID]
		cMaxCons := classMaxCons[cID]
		tMaxDay := teacherMaxDay[tID]
		cMaxDay := classMaxDay[cID]
		for _, cc := range cands {
			if teacherBusy[tID][cc.day][cc.slot] {
				continue
			}
			if !classFree(cset, classBusy, cc.day, cc.slot) {
				continue
			}
			if roomBusy[cc.room][cc.day][cc.slot] {
				continue
			}
			if tMaxDay > 0 && teacherDay[tID][cc.day]+1 > tMaxDay {
				continue
			}
			if cMaxDay > 0 && classDay[cID][cc.day]+1 > cMaxDay {
				continue
			}
			if tMaxCons > 0 && runIfPlaced(teacherBusy[tID], cc.day, cc.slot) > tMaxCons {
				continue
			}
			if cMaxCons > 0 && runIfPlaced(classBusy[cID], cc.day, cc.slot) > cMaxCons {
				continue
			}
			teacherBusy[tID][cc.day][cc.slot] = true
			for _, cid := range cset {
				classBusy[cid][cc.day][cc.slot] = true
			}
			roomBusy[cc.room][cc.day][cc.slot] = true
			teacherDay[tID][cc.day]++
			classDay[cID][cc.day]++
			assign[oi] = cc
			if rec(k + 1) {
				return true
			}
			teacherBusy[tID][cc.day][cc.slot] = false
			for _, cid := range cset {
				classBusy[cid][cc.day][cc.slot] = false
			}
			roomBusy[cc.room][cc.day][cc.slot] = false
			teacherDay[tID][cc.day]--
			classDay[cID][cc.day]--
		}
		return false
	}
	rec(0)

	if ok {
		entries := buildEntries(in, solution, occ)
		return Result{Entries: entries, Placed: len(occ), Total: len(occ), Violations: softViolations(in, entries, days)}
	}
	return Result{Entries: nil, Placed: 0, Total: len(occ)}
}

type cell struct {
	day, slot, room int
}

func candidateCells(o Occurrence, hard HardSet, days, slots int, classBusy map[int][][]bool, classSet []int, roomBusy map[int][][]bool) []cell {
	var out []cell
	for d := 0; d < days; d++ {
		for s := 0; s < slots; s++ {
			if hard.Forbidden(o.Lesson.TeacherID, o.Lesson.ClassID, 0, d, s) {
				continue
			}
			free := true
			for _, cid := range classSet {
				if classBusy[cid][d][s] {
					free = false
					break
				}
			}
			if !free {
				continue
			}
			for _, r := range o.RoomChoices {
				if roomBusy[r][d][s] {
					continue
				}
				out = append(out, cell{day: d, slot: s, room: r})
			}
		}
	}
	return out
}

// HardSet holds precomputed forbidden (teacher/class/room, day, slot) cells.
type HardSet struct {
	teacher map[int]map[int]bool // teacherID -> day*slots+slot
	class   map[int]map[int]bool
	room    map[int]map[int]bool
}

func (h HardSet) Forbidden(teacherID, classID, roomID, day, slot int) bool {
	key := day*1000 + slot
	if h.teacher[teacherID][key] {
		return true
	}
	if h.class[classID][key] {
		return true
	}
	if roomID != 0 && h.room[roomID][key] {
		return true
	}
	return false
}

func buildHard(in SolveInput, days, slots int) HardSet {
	h := HardSet{
		teacher: map[int]map[int]bool{},
		class:   map[int]map[int]bool{},
		room:    map[int]map[int]bool{},
	}
	for _, c := range in.Constraints {
		if !c.IsHard {
			continue
		}
		if c.Type != "teacher_unavailable" && c.Type != "class_unavailable" && c.Type != "room_unavailable" {
			continue
		}
		dow := -1
		if c.DayOfWeek != nil {
			dow = *c.DayOfWeek
		}
		ts := -1
		if c.TimeslotStart != nil {
			ts = *c.TimeslotStart
		}
		target := map[int]map[int]bool{}
		switch c.EntityType {
		case "teacher":
			target = h.teacher
		case "class":
			target = h.class
		case "room":
			target = h.room
		}
		if target[c.EntityID] == nil {
			target[c.EntityID] = map[int]bool{}
		}
		if dow >= 0 && ts >= 0 {
			target[c.EntityID][dow*1000+ts] = true
		} else if dow >= 0 {
			for s := 0; s < slots; s++ {
				target[c.EntityID][dow*1000+s] = true
			}
		} else if ts >= 0 {
			for d := 0; d < days; d++ {
				target[c.EntityID][d*1000+ts] = true
			}
		} else {
			for d := 0; d < days; d++ {
				for s := 0; s < slots; s++ {
					target[c.EntityID][d*1000+s] = true
				}
			}
		}
	}
	return h
}

func buildOccurrences(in SolveInput, days, slots int) []Occurrence {
	var occ []Occurrence
	for _, l := range in.Lessons {
		h := l.HoursPerWeek
		if h < 1 {
			h = 1
		}
		subj := in.Subjects[l.SubjectID]
		rooms := allowedRooms(l, subj, in.Rooms)
		for i := 0; i < h; i++ {
			occ = append(occ, Occurrence{Lesson: l, Index: i, RoomChoices: rooms})
		}
	}
	return occ
}

func allowedRooms(l domain.Lesson, subj domain.Subject, rooms []domain.Room) []int {
	var preferred []int
	if l.PreferredRooms != "" && l.PreferredRooms != "[]" {
		_ = json.Unmarshal([]byte(l.PreferredRooms), &preferred)
	}
	if len(preferred) > 0 {
		return preferred
	}
	rt := subj.RequiresRoomType
	if rt == "" {
		rt = "any"
	}
	var out []int
	for _, r := range rooms {
		if rt == "any" || r.RoomType == "any" || r.RoomType == rt {
			out = append(out, r.ID)
		}
	}
	if len(out) == 0 && len(rooms) > 0 {
		for _, r := range rooms {
			out = append(out, r.ID)
		}
	}
	return out
}

// softViolations counts soft-constraint penalties (gaps between same-class lessons, preferences).
func softViolations(in SolveInput, entries []domain.ScheduleEntry, days int) int {
	v := 0
	classDays := map[int]map[int]bool{}
	for _, e := range entries {
		if classDays[e.ClassID] == nil {
			classDays[e.ClassID] = map[int]bool{}
		}
		classDays[e.ClassID][e.DayOfWeek] = true
	}
	// min_gap_days penalty: a class should not be scheduled every day if min_gap requested
	lessonByClass := map[int]int{} // class -> lessons count
	for _, l := range in.Lessons {
		lessonByClass[l.ClassID]++
	}
	for classID, dmap := range classDays {
		// if 6 days used and there are lessons, that's fine; gap only matters for few-hours lessons
		if len(dmap) >= days {
			// every day used; if any lesson for this class has min_gap_days>1, penalize lightly
			for _, l := range in.Lessons {
				if l.ClassID == classID && l.MinGapDays > 1 {
					v += 1
				}
			}
		}
	}
	// soft constraints: prefer_morning / max_gaps style => small penalties for afternoon clustering
	for _, c := range in.Constraints {
		if c.IsHard {
			continue
		}
		switch c.Type {
		case "prefer_morning":
			for _, e := range entries {
				if matchesEntity(c, e) && e.Timeslot >= 4 {
					v += 1
				}
			}
		case "max_gaps":
			// count gaps per day per class
			gaps := gapCount(entries, c.EntityID)
			if gaps > c.Weight {
				v += gaps - c.Weight
			}
		case "min_lessons_per_day":
			byDay := map[int]int{}
			for _, e := range entries {
				if matchesEntity(c, e) {
					byDay[e.DayOfWeek]++
				}
			}
			for d := 0; d < days; d++ {
				if byDay[d] < c.Weight {
					v += c.Weight - byDay[d]
				}
			}
		}
	}
	return v
}

func matchesEntity(c domain.Constraint, e domain.ScheduleEntry) bool {
	switch c.EntityType {
	case "teacher":
		return e.TeacherID == c.EntityID
	case "class":
		return e.ClassID == c.EntityID
	case "room":
		return e.RoomID == c.EntityID
	case "lesson":
		return e.LessonID == c.EntityID
	case "school":
		return true
	}
	return false
}

func gapCount(entries []domain.ScheduleEntry, classID int) int {
	byDay := map[int][]int{}
	for _, e := range entries {
		if e.ClassID != classID {
			continue
		}
		byDay[e.DayOfWeek] = append(byDay[e.DayOfWeek], e.Timeslot)
	}
	total := 0
	for _, slots := range byDay {
		if len(slots) < 2 {
			continue
		}
		sort.Ints(slots)
		for i := 1; i < len(slots); i++ {
			if slots[i]-slots[i-1] > 1 {
				total++
			}
		}
	}
	return total
}

func newGrid(days, slots int) [][]bool {
	g := make([][]bool, days)
	for i := range g {
		g[i] = make([]bool, slots)
	}
	return g
}

func shuffle(c []cell, rng *rand.Rand) {
	rng.Shuffle(len(c), func(i, j int) { c[i], c[j] = c[j], c[i] })
}

// buildStudentSets returns, for each class, the set of class ids whose student bodies overlap.
// A parent class shares students with all its subgroups; two subgroups of the same parent are disjoint.
func buildStudentSets(classes map[int]domain.SchoolClass) map[int][]int {
	children := map[int][]int{}
	for id, c := range classes {
		if c.SubgroupOf != nil {
			children[*c.SubgroupOf] = append(children[*c.SubgroupOf], id)
		}
	}
	res := map[int][]int{}
	for id := range classes {
		set := []int{id}
		if subs, ok := children[id]; ok {
			set = append(set, subs...)
		}
		res[id] = set
	}
	return res
}

func classFree(cset []int, classBusy map[int][][]bool, d, s int) bool {
	for _, cid := range cset {
		if classBusy[cid][d][s] {
			return false
		}
	}
	return true
}

// runIfPlaced returns the length of the consecutive occupied run through (day,slot) if it were occupied.
func runIfPlaced(g [][]bool, day, slot int) int {
	cnt := 1
	for s := slot - 1; s >= 0 && g[day][s]; s-- {
		cnt++
	}
	for s := slot + 1; s < len(g[day]) && g[day][s]; s++ {
		cnt++
	}
	return cnt
}

func buildEntries(in SolveInput, assign []cell, occ []Occurrence) []domain.ScheduleEntry {
	entries := make([]domain.ScheduleEntry, 0, len(occ))
	for i := range occ {
		c := assign[i]
		if c.room == 0 {
			continue
		}
		o := occ[i]
		entries = append(entries, domain.ScheduleEntry{
			SchoolID:  in.SchoolID,
			LessonID:  o.Lesson.ID,
			ClassID:   o.Lesson.ClassID,
			TeacherID: o.Lesson.TeacherID,
			SubjectID: o.Lesson.SubjectID,
			RoomID:    c.room,
			DayOfWeek: c.day,
			Timeslot:  c.slot,
			WeekType:  0,
		})
	}
	return entries
}

// validateAggregates checks day-level hard constraints that cannot be enforced incrementally.
func validateAggregates(in SolveInput, ss map[int][]int, entries []domain.ScheduleEntry, days, slots int) bool {
	for _, c := range in.Constraints {
		if !c.IsHard {
			continue
		}
		if c.Type == "lunch_break" {
			if !lunchOK(in, ss, c, entries, days, slots) {
				return false
			}
		}
	}
	return true
}

func lunchOK(in SolveInput, ss map[int][]int, c domain.Constraint, entries []domain.ScheduleEntry, days, slots int) bool {
	occ := make([][]bool, days)
	for d := range occ {
		occ[d] = make([]bool, slots)
	}
	for _, e := range entries {
		var inEnt bool
		switch c.EntityType {
		case "school":
			inEnt = true
		case "teacher":
			inEnt = e.TeacherID == c.EntityID
		case "class":
			inEnt = sharesStudent(ss, c.EntityID, e.ClassID)
		}
		if inEnt && e.DayOfWeek < days && e.Timeslot < slots {
			occ[e.DayOfWeek][e.Timeslot] = true
		}
	}
	start, end := 0, slots-1
	if c.TimeslotStart != nil {
		start = *c.TimeslotStart
	}
	if c.TimeslotEnd != nil {
		end = *c.TimeslotEnd
	}
	if end >= slots {
		end = slots - 1
	}
	for d := 0; d < days; d++ {
		free := false
		for s := start; s <= end; s++ {
			if !occ[d][s] {
				free = true
				break
			}
		}
		if !free {
			return false
		}
	}
	return true
}

func sharesStudent(ss map[int][]int, a, b int) bool {
	sa := ss[a]
	if sa == nil {
		sa = []int{a}
	}
	sb := ss[b]
	if sb == nil {
		sb = []int{b}
	}
	set := map[int]bool{}
	for _, x := range sa {
		set[x] = true
	}
	for _, x := range sb {
		if set[x] {
			return true
		}
	}
	return false
}

// preciseSolver is assigned by the OR-Tools build-tagged file (internal/solver/ortools_csat.go)
// when the binary is compiled with `-tags ortools`. It is nil otherwise.
var preciseSolver func(in SolveInput, parallelism int, timeout time.Duration) (Result, bool)

// HasPreciseSolver reports whether the running binary was compiled with the
// OR-Tools CP-SAT solver (-tags ortools). When false, SolvePrecise silently
// falls back to the pure-Go backtracking solver. The frontend uses this to
// tell the user the truth instead of flashing "CP-SAT enabled" when the
// heavy C++ dependency is not actually linked in.
func HasPreciseSolver() bool {
	return preciseSolver != nil
}

// SolvePrecise prefers the OR-Tools CP-SAT solver when compiled in; otherwise falls back to the
// pure-Go backtracking solver so the default build always works.
func SolvePrecise(ctx context.Context, in SolveInput, parallelism int, timeout time.Duration) Result {
	if preciseSolver != nil {
		if r, ok := preciseSolver(in, parallelism, timeout); ok {
			return r
		}
	}
	return Solve(ctx, in, parallelism, timeout)
}
