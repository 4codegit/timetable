//go:build windows && ortools

package solver

import (
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"timetable/internal/domain"
)

// Mirror of C structs in bind.h (field order + alignment must match).
type cConstraint struct {
	ctype      int32
	entityType int32
	entityID   int32
	day        int32
	slotStart  int32
	slotEnd    int32
	value      int32
	isHard     int32
}

type cScheduleResult struct {
	count      int32
	lessonIDs  *int32
	classIDs   *int32
	teacherIDs *int32
	subjectIDs *int32
	roomIDs    *int32
	days       *int32
	slots      *int32
}

type ortoolsDLL struct {
	mod    *syscall.LazyDLL
	solve  *syscall.LazyProc
	freeFn *syscall.LazyProc
}

var loadedDLL *ortoolsDLL

func loadDLL() (*ortoolsDLL, error) {
	if loadedDLL != nil {
		return loadedDLL, nil
	}
	mod := syscall.NewLazyDLL("ortools_csat.dll")
	d := &ortoolsDLL{
		mod:    mod,
		solve:  mod.NewProc("ortools_solve"),
		freeFn: mod.NewProc("free_schedule_result"),
	}
	if err := mod.Load(); err != nil {
		return nil, err
	}
	loadedDLL = d
	return d, nil
}

func init() {
	preciseSolver = ortoolsSolve
}

func ortoolsSolve(in SolveInput, parallelism int, timeout time.Duration) (Result, bool) {
	d, err := loadDLL()
	if err != nil {
		return Result{}, false
	}

	days := int32(in.Config.DaysPerWeek)
	if days <= 0 {
		days = 6
	}
	slots := int32(in.Config.SlotsPerDay)
	if slots <= 0 {
		slots = 8
	}

	nl := len(in.Lessons)
	hours := make([]int32, maxInt(nl, 1))
	lclass := make([]int32, maxInt(nl, 1))
	lteacher := make([]int32, maxInt(nl, 1))
	lsubject := make([]int32, maxInt(nl, 1))
	for i, l := range in.Lessons {
		h := int32(l.HoursPerWeek)
		if h < 1 {
			h = 1
		}
		hours[i] = h
		lclass[i] = int32(l.ClassID)
		lteacher[i] = int32(l.TeacherID)
		lsubject[i] = int32(l.SubjectID)
	}

	roomTypeID := map[string]int32{"any": 0}
	next := int32(1)
	assignType := func(s string) int32 {
		if s == "" {
			return 0
		}
		if id, ok := roomTypeID[s]; ok {
			return id
		}
		id := next
		next++
		roomTypeID[s] = id
		return id
	}
	lreq := make([]int32, maxInt(nl, 1))
	for i, l := range in.Lessons {
		rt := "any"
		if subj, ok := in.Subjects[l.SubjectID]; ok && subj.RequiresRoomType != "" {
			rt = subj.RequiresRoomType
		}
		lreq[i] = assignType(rt)
	}

	nr := len(in.Rooms)
	roomIDs := make([]int32, maxInt(nr, 1))
	roomTypes := make([]int32, maxInt(nr, 1))
	for i, r := range in.Rooms {
		roomIDs[i] = int32(r.ID)
		roomTypes[i] = assignType(r.RoomType)
	}

	cts := make([]cConstraint, maxInt(len(in.Constraints), 1))
	for i, c := range in.Constraints {
		cts[i].ctype = int32(constraintTypeCode(c.Type))
		cts[i].entityType = int32(entityTypeCode(c.EntityType))
		cts[i].entityID = int32(c.EntityID)
		if c.DayOfWeek != nil {
			cts[i].day = int32(*c.DayOfWeek)
		} else {
			cts[i].day = -1
		}
		if c.TimeslotStart != nil {
			cts[i].slotStart = int32(*c.TimeslotStart)
		} else {
			cts[i].slotStart = -1
		}
		if c.TimeslotEnd != nil {
			cts[i].slotEnd = int32(*c.TimeslotEnd)
		} else {
			cts[i].slotEnd = -1
		}
		cts[i].value = int32(c.Weight)
		if c.IsHard {
			cts[i].isHard = 1
		} else {
			cts[i].isHard = 0
		}
	}

	workers := int32(parallelism)
	if workers <= 0 {
		workers = int32(runtime.NumCPU())
	}
	tlim := int32(timeout.Milliseconds())

	r1, _, _ := d.solve.Call(
		uintptr(nl),
		uintptr(unsafe.Pointer(&hours[0])),
		uintptr(unsafe.Pointer(&lclass[0])),
		uintptr(unsafe.Pointer(&lteacher[0])),
		uintptr(unsafe.Pointer(&lsubject[0])),
		uintptr(unsafe.Pointer(&lreq[0])),
		uintptr(nr),
		uintptr(unsafe.Pointer(&roomIDs[0])),
		uintptr(unsafe.Pointer(&roomTypes[0])),
		uintptr(days),
		uintptr(slots),
		uintptr(len(in.Constraints)),
		uintptr(unsafe.Pointer(&cts[0])),
		uintptr(tlim),
		uintptr(workers),
	)
	if r1 == 0 {
		return Result{}, false
	}
	res := (*cScheduleResult)(unsafe.Pointer(r1))
	defer d.freeFn.Call(r1)

	n := int(res.count)
	lessonIDs := unsafe.Slice((*int32)(res.lessonIDs), n)
	classIDs := unsafe.Slice((*int32)(res.classIDs), n)
	teacherIDs := unsafe.Slice((*int32)(res.teacherIDs), n)
	subjectIDs := unsafe.Slice((*int32)(res.subjectIDs), n)
	roomIDsOut := unsafe.Slice((*int32)(res.roomIDs), n)
	dayArr := unsafe.Slice((*int32)(res.days), n)
	slotArr := unsafe.Slice((*int32)(res.slots), n)

	entries := make([]domain.ScheduleEntry, 0, n)
	for i := 0; i < n; i++ {
		// When no rooms are defined the solver outputs room_id=0 which
		// does not exist in the database — skip to avoid FK constraint error.
		if len(in.Rooms) == 0 && int(roomIDsOut[i]) == 0 {
			continue
		}
		entries = append(entries, domain.ScheduleEntry{
			SchoolID:  in.SchoolID,
			LessonID:  int(lessonIDs[i]),
			ClassID:   int(classIDs[i]),
			TeacherID: int(teacherIDs[i]),
			SubjectID: int(subjectIDs[i]),
			RoomID:    int(roomIDsOut[i]),
			DayOfWeek: int(dayArr[i]),
			Timeslot:  int(slotArr[i]),
			WeekType:  0,
		})
	}
	return Result{Entries: entries, Placed: len(entries), Total: n, Violations: 0}, true
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
