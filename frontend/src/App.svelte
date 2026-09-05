<script>
        import {
                Greet, CreateSchool, ListSchools, DeleteSchool, SchoolHasLessons, SchoolHasSchedule,
                CreateTeacher, ListTeachers, CreateSubject, ListSubjects,
                CreateClass, ListClasses, CreateRoom, ListRooms,
                CreateLesson, ListLessons, DeleteLesson, UpdateLesson,
                CreateConstraint, ListConstraints, DeleteConstraint,
                DeleteTeacher, DeleteSubject, DeleteClass, DeleteRoom, DeleteScheduleEntry, SaveExport,
                Generate, GeneratePrecise, MoveEntry, SwapEntries, ReplaceSchedule, ListSchedule, ExportAll, ImportAll, ScheduleCSV, ExportRefsCSV, ImportRefsCSV, GetSchoolSettings, UpdateSchoolSettings, HasPreciseSolver
        } from "../wailsjs/go/main/App";
        import { jsPDF } from "jspdf";
        import { onMount } from "svelte";

        let schools = [];
        let activeSchoolID = 0;
        let newSchoolName = "Моя школа";
        let tab = "refs";
        const APP_VERSION = "1.6.17";
        let msg = "";

        let teachers = [], subjects = [], classes = [], rooms = [], lessons = [], constraints = [], schedule = [];

        // form models
        let t = { name: "", short_name: "", max_hours_per_week: 30 };
        let s = { name: "", short_name: "", requires_room_type: "any" };
        let c = { name: "", grade: 0, student_count: 0, subgroup_of: null };
        let r = { name: "", capacity: 30, room_type: "any" };
        let l = { class_id: 0, subject_id: 0, teacher_id: 0, hours_per_week: 1, min_gap_days: 1, can_split: false, preferred_rooms: "[]" };
        let curClass = 0;
        let con = { type: "teacher_unavailable", entity_type: "teacher", entity_id: 0, day_of_week: null, timeslot_start: null, timeslot_end: null, weight: 100, is_hard: true };
        function resetConstraintFields() {
                // Clear fields that are not relevant for the newly-selected constraint type
                // so we don't accidentally send stale values to the solver.
                const f = constraintFields(con.type);
                if (!f.day) con.day_of_week = null;
                if (!f.slots) { con.timeslot_start = null; con.timeslot_end = null; }
                if (!f.value) con.weight = 100;
        }

        let days = 6, slots = 8;
        let bellPeriods = [];
        let genResult = null;
        let usePrecise = false;
        // hasPreciseSolver is loaded once from the backend so the UI can tell the
        // user the truth: if the binary was NOT compiled with -tags ortools, the
        // "точный CP-SAT (OR-Tools)" checkbox silently runs the pure-Go fallback.
        let hasPreciseSolver = true;
        async function detectPreciseSolver() {
                try { hasPreciseSolver = await HasPreciseSolver(); }
                catch (e) { hasPreciseSolver = true; /* old binary: assume yes */ }
        }
        let viewMode = "class";
        $: rows = viewMode === "teacher"
                ? teachers.map((t) => ({ id: t.id, label: t.name }))
                : viewMode === "room"
                ? rooms.map((r) => ({ id: r.id, label: r.name }))
                : classes.map((c) => ({ id: c.id, label: c.name }));

        let exportMode = "school";
        let pageSize = "A2";
        let orientation = "landscape";
        let compact = false;
        let pdfShowTeacher = true;
        let pdfShowRoom = true;
        let pdfWeekdaysOnly = false;
        let pdfBW = false;
        let classPage = 0;
        const classesPerPage = 10;
        $: visibleRows = viewMode === "class"
                ? rows.slice(classPage * classesPerPage, classPage * classesPerPage + classesPerPage)
                : rows;
        $: totalClassPages = Math.max(1, Math.ceil(classes.length / classesPerPage));

        function flash(m) { msg = m; setTimeout(() => msg = "", 3000); }

        async function loadSchools() {
                schools = (await ListSchools()) || [];
                if (schools.length && !activeSchoolID) activeSchoolID = schools[0].id;
                await loadSettings();
        }
        async function createSchool() {
                if (!newSchoolName.trim()) { flash("Введите название школы"); return; }
                const sc = await CreateSchool(newSchoolName);
                activeSchoolID = sc.id;
                await loadSchools();
                flash("Школа создана");
        }
        async function loadSettings() {
                if (!activeSchoolID) return;
                try {
                        const raw = await GetSchoolSettings(activeSchoolID);
                        const st = raw ? JSON.parse(raw) : {};
                        if (st.days > 0) days = st.days;
                        if (st.slots > 0) slots = st.slots;
                        if (Array.isArray(st.periods) && st.periods.length === slots) {
                                bellPeriods = st.periods;
                        } else {
                                bellPeriods = Array.from({ length: slots }, () => ({ start: "", end: "" }));
                        }
                } catch (e) {
                        bellPeriods = Array.from({ length: slots }, () => ({ start: "", end: "" }));
                }
        }
        async function saveSettings() {
                const periods = [];
                for (let i = 0; i < slots; i++) periods.push(bellPeriods[i] || { start: "", end: "" });
                const st = { days, slots, periods };
                await UpdateSchoolSettings(activeSchoolID, JSON.stringify(st));
                bellPeriods = periods;
                flash("Настройки сохранены");
        }
        function onSlotsChange() {
                const cur = bellPeriods.slice();
                while (cur.length < slots) cur.push({ start: "", end: "" });
                bellPeriods = cur.slice(0, slots);
        }
        async function reloadRefs() {
                if (!activeSchoolID) return;
                const res = await Promise.all([
                        ListTeachers(activeSchoolID), ListSubjects(activeSchoolID),
                        ListClasses(activeSchoolID), ListRooms(activeSchoolID),
                        ListLessons(activeSchoolID), ListConstraints(activeSchoolID)
                ]);
                [teachers, subjects, classes, rooms, lessons, constraints] = res.map((x) => x || []);
        }
        async function reloadSchedule() {
                if (!activeSchoolID) return;
                schedule = (await ListSchedule(activeSchoolID)) || [];
                recomputeConflicts();
        }

        let conflictIDs = new Set();
        function recomputeConflicts() {
                const maps = { teacher_id: {}, class_id: {}, room_id: {} };
                for (const e of schedule) {
                        const k = e.day_of_week * 1000 + e.timeslot;
                        for (const f of ["teacher_id", "class_id", "room_id"]) {
                                if (!maps[f][k]) maps[f][k] = [];
                                maps[f][k].push(e.id);
                        }
                }
                const ids = new Set();
                for (const f in maps) {
                        for (const k in maps[f]) {
                                if (maps[f][k].length > 1) maps[f][k].forEach((id) => ids.add(id));
                        }
                }
                conflictIDs = ids;
        }

        let report = { conflicts: [], unplaced: [], overloads: [] };
        function computeConflictReport() {
                const byKey = { teacher_id: {}, class_id: {}, room_id: {} };
                for (const e of schedule) {
                        const key = e.day_of_week * 1000 + e.timeslot;
                        for (const f of ["teacher_id", "class_id", "room_id"]) {
                                (byKey[f][key] = byKey[f][key] || []).push(e);
                        }
                }
                const labels = { teacher_id: "Учитель", class_id: "Класс", room_id: "Кабинет" };
                const conflicts = [];
                for (const f of ["teacher_id", "class_id", "room_id"]) {
                        for (const key in byKey[f]) {
                                const arr = byKey[f][key];
                                if (arr.length > 1) {
                                        const day = Math.floor(key / 1000), slot = key % 1000;
                                        conflicts.push({
                                                type: labels[f], day, slot,
                                                items: arr.map((e) => ({
                                                        subject: subjName(subjects, e.subject_id),
                                                        who: f === "teacher_id" ? teachName(teachers, e.teacher_id)
                                                                : f === "class_id" ? className(classes, e.class_id)
                                                                : (rooms.find((r) => r.id === e.room_id)?.name || "?")
                                                }))
                                        });
                                }
                        }
                }
                const placedByLesson = {};
                for (const e of schedule) placedByLesson[e.lesson_id] = (placedByLesson[e.lesson_id] || 0) + 1;
                const unplaced = lessons.filter((l) => (placedByLesson[l.id] || 0) < (l.hours_per_week || 0))
                        .map((l) => ({ subject: subjName(subjects, l.subject_id), cls: className(classes, l.class_id), need: l.hours_per_week, got: placedByLesson[l.id] || 0 }));
                const teacherHours = {};
                for (const e of schedule) teacherHours[e.teacher_id] = (teacherHours[e.teacher_id] || 0) + 1;
                const overloads = teachers.filter((t) => t.max_hours_per_week && (teacherHours[t.id] || 0) > t.max_hours_per_week)
                        .map((t) => ({ name: t.name, max: t.max_hours_per_week, got: teacherHours[t.id] || 0 }));
                return { conflicts, unplaced, overloads };
        }
        $: report = computeConflictReport();

        let history = [];
        async function pushHistory() {
                history.push(JSON.parse(JSON.stringify(schedule)));
                if (history.length > 50) history.shift();
        }
        async function undo() {
                if (!history.length) return;
                const prev = history.pop();
                await ReplaceSchedule(activeSchoolID, prev);
                await reloadSchedule();
                flash("Отменено");
        }

        $: if (activeSchoolID) { reloadRefs(); reloadSchedule(); }

        async function addTeacher() {
                if (!t.name.trim()) { flash("Введите имя учителя"); return; }
                await CreateTeacher({ ...t, school_id: activeSchoolID });
                t = { name: "", short_name: "", max_hours_per_week: 30 };
                await reloadRefs();
        }
        async function addSubject() {
                if (!s.name.trim()) { flash("Введите название предмета"); return; }
                await CreateSubject({ ...s, school_id: activeSchoolID });
                s = { name: "", short_name: "", requires_room_type: "any" };
                await reloadRefs();
        }
        async function addClass() {
                if (!c.name.trim()) { flash("Введите название класса"); return; }
                await CreateClass({ ...c, school_id: activeSchoolID });
                c = { name: "", grade: 0, student_count: 0, subgroup_of: null };
                await reloadRefs();
        }
        async function addRoom() {
                if (!r.name.trim()) { flash("Введите название кабинета"); return; }
                await CreateRoom({ ...r, school_id: activeSchoolID });
                r = { name: "", capacity: 30, room_type: "any" };
                await reloadRefs();
        }
        async function addLesson() {
                if (!l.class_id || !l.subject_id || !l.teacher_id) { flash("Выберите класс, предмет и учителя"); return; }
                const hours = Math.max(1, Math.min(40, l.hours_per_week || 1));
                await CreateLesson({ ...l, school_id: activeSchoolID, hours_per_week: hours });
                l = { class_id: 0, subject_id: 0, teacher_id: 0, hours_per_week: 1, min_gap_days: 1, can_split: false, preferred_rooms: "[]" };
                await reloadRefs();
        }
        async function addLessonForClass() {
                if (!curClass || !l.subject_id || !l.teacher_id) { flash("Выберите класс, предмет и учителя"); return; }
                await CreateLesson({ school_id: activeSchoolID, class_id: curClass, subject_id: l.subject_id, teacher_id: l.teacher_id, hours_per_week: l.hours_per_week || 1, min_gap_days: l.min_gap_days || 1, can_split: false, preferred_rooms: "[]" });
                l = { class_id: 0, subject_id: 0, teacher_id: 0, hours_per_week: 1, min_gap_days: 1, can_split: false, preferred_rooms: "[]" };
                await reloadRefs();
        }
        async function updateLesson(x) {
                try {
                        await UpdateLesson({ id: x.id, school_id: x.school_id, class_id: x.class_id, subject_id: x.subject_id, teacher_id: x.teacher_id, hours_per_week: x.hours_per_week || 1, min_gap_days: x.min_gap_days || 1, can_split: x.can_split, preferred_rooms: x.preferred_rooms || "[]" });
                } catch (e) { flash("Ошибка обновления урока: " + (e && e.message ? e.message : e)); }
        }
        async function addConstraint() {
                const payload = { ...con, school_id: activeSchoolID };
                if (payload.entity_type === "school") payload.entity_id = activeSchoolID;
                if (payload.day_of_week === null || payload.day_of_week === "") delete payload.day_of_week;
                if (payload.timeslot_start === null || payload.timeslot_start === "") delete payload.timeslot_start;
                if (payload.timeslot_end === null || payload.timeslot_end === "") delete payload.timeslot_end;
                await CreateConstraint(payload);
                await reloadRefs();
        }
        async function removeLesson(id) { if (!await confirmAction("Удалить урок?")) return; await pushHistory(); await DeleteLesson(id); await reloadRefs(); flash("Урок удалён"); }
        async function confirmAction(message) {
                return window.confirm(message);
        }
        async function removeTeacher(id) { if (!await confirmAction("Удалить учителя? Все его уроки тоже будут удалены.")) return; await pushHistory(); await DeleteTeacher(id); await reloadRefs(); flash("Учитель удалён"); }
        async function removeSubject(id) { if (!await confirmAction("Удалить предмет? Связанные уроки тоже будут удалены.")) return; await pushHistory(); await DeleteSubject(id); await reloadRefs(); flash("Предмет удалён"); }
        async function removeClass(id) { if (!await confirmAction("Удалить класс? Все уроки и расписание класса будут удалены.")) return; await pushHistory(); await DeleteClass(id); await reloadRefs(); flash("Класс удалён"); }
        async function removeRoom(id) { if (!await confirmAction("Удалить кабинет? Связанные ячейки расписания тоже будут удалены.")) return; await pushHistory(); await DeleteRoom(id); await reloadRefs(); await reloadSchedule(); flash("Кабинет удалён"); }
        async function removeConstraint(id) { await DeleteConstraint(id); await reloadRefs(); flash("Ограничение удалено"); }
        async function removeEntry(id) { await pushHistory(); await DeleteScheduleEntry(id); await reloadSchedule(); }

        async function generate() {
                await pushHistory();
                if (!lessons.length) { flash("Нет уроков в учебном плане — добавьте их на вкладке «Уроки»."); history.pop(); return; }
                const occurrences = lessons.reduce((a, l) => a + (l.hours_per_week || 0), 0);
                if (!usePrecise && occurrences > 200) {
                        usePrecise = true;
                        if (hasPreciseSolver) {
                                flash("Крупная школа: включён точный CP-SAT (OR-Tools).");
                        } else {
                                flash("Крупная школа: OR-Tools недоступен в этой сборке — используется быстрый эвристический решатель (может не разместить все уроки).");
                        }
                }
                try {
                        genResult = (usePrecise ? await GeneratePrecise(activeSchoolID, days, slots) : await Generate(activeSchoolID, days, slots)) || {};
                        await reloadSchedule();
                        flash(`Размещено ${genResult.placed}/${genResult.total}, нарушений (мягких): ${genResult.violations}`);
                } catch (e) {
                        flash("Ошибка генерации: " + (e && e.message ? e.message : e));
                }
        }
        async function generatePrecise() {
                await pushHistory();
                if (!lessons.length) { flash("Нет уроков в учебном плане — добавьте их на вкладке «Уроки»."); history.pop(); return; }
                try {
                        genResult = await GeneratePrecise(activeSchoolID, days, slots);
                        await reloadSchedule();
                        flash(`CP-SAT: размещено ${genResult.placed}/${genResult.total}`);
                } catch (e) {
                        flash("Ошибка генерации: " + (e && e.message ? e.message : e));
                }
        }
        async function applyMove(id, kind, rowId, day, slot) {
                const src = schedule.find(en => en.id === id);
                if (!src) {
                        flash("⚠ Не найден исходный урок — возможно, расписание изменилось. Обновите вкладку.");
                        return;
                }
                // Source's own row identifier (its class/teacher/room id).
                const srcRowId = kind === "class" ? src.class_id : kind === "teacher" ? src.teacher_id : src.room_id;
                if (srcRowId !== rowId) {
                        const rowKindLabel = kind === "class" ? "классами" : kind === "teacher" ? "учителями" : "кабинетами";
                        flash(`⚠ Нельзя перемещать урок между ${rowKindLabel} (это нарушило бы структуру расписания). Только в пределах одной строки.`);
                        return;
                }
                await pushHistory();
                // Find target in same row at the destination cell.
                const target = schedule.find(en => {
                        if (en.id === id) return false;
                        if (en.day_of_week !== day || en.timeslot !== slot) return false;
                        if (kind === "class") return en.class_id === rowId;
                        if (kind === "teacher") return en.teacher_id === rowId;
                        return en.room_id === rowId;
                });
                try {
                        if (target) {
                                await SwapEntries(src.id, day, slot, target.id, src.day_of_week, src.timeslot);
                                // Update the displayed grid before the database refresh.  This makes a
                                // successful drag visible even when a slow Wails/SQLite round trip follows.
                                schedule = schedule.map((en) => en.id === src.id
                                        ? { ...en, day_of_week: day, timeslot: slot }
                                        : en.id === target.id
                                        ? { ...en, day_of_week: src.day_of_week, timeslot: src.timeslot }
                                        : en);
                                recomputeConflicts();
                                flash(`✓ Поменяли местами: ${cellLabelShort(src)} (${dayName(src.day_of_week)} П${src.timeslot + 1}) ⟷ ${cellLabelShort(target)} (${dayName(day)} П${slot + 1})`);
                        } else {
                                await MoveEntry(id, day, slot);
                                // Do the same for a move into an empty cell; reloadSchedule below still
                                // reconciles the local view with the persisted data.
                                schedule = schedule.map((en) => en.id === id
                                        ? { ...en, day_of_week: day, timeslot: slot }
                                        : en);
                                recomputeConflicts();
                                flash(`✓ Перемещено: ${cellLabelShort(src)} → ${dayName(day)} П${slot + 1}`);
                        }
                } catch (err) {
                        flash(`⚠ Не удалось переместить: ${err && err.message ? err.message : err}`);
                }
                try { await reloadSchedule(); }
                catch (err) { flash("Изменение сохранено, но не удалось обновить таблицу: " + (err && err.message ? err.message : err)); }
        }
        let selectedEntry = null;
        let pendingDrag = null;
        let startPos = null;
        let dragging = false;
        let ghost = null;

        // Engine-independent hit-test: find the schedule cell whose bounding rect
        // contains the pointer. Avoids elementFromPoint, which is unreliable inside
        // the Wails WebKit webview (especially during pointer capture).
        function hitCell(x, y) {
                const tds = document.querySelectorAll("td[data-cell]");
                for (let i = 0; i < tds.length; i++) {
                        const r = tds[i].getBoundingClientRect();
                        if (x >= r.left && x <= r.right && y >= r.top && y <= r.bottom) return tds[i];
                }
                return null;
        }

        function onPointerDown(e, cell, kind, rowId, day, slot) {
                if (e.button !== 0) return;
                if (e.target && e.target.closest && e.target.closest(".cell-x")) return;
                e.preventDefault();
                startPos = { x: e.clientX, y: e.clientY };
                pendingDrag = { id: cell ? cell.id : null, kind, rowId, day, slot, label: cell ? cell.label : "" };
                dragging = false;
        }
        function onPointerMove(e) {
                if (!pendingDrag || !startPos) return;
                const dx = e.clientX - startPos.x, dy = e.clientY - startPos.y;
                if (!dragging && Math.hypot(dx, dy) > 6) {
                        dragging = true;
                }
                if (dragging) {
                        ghost = { label: pendingDrag.label, x: e.clientX, y: e.clientY };
                }
        }
        async function onPointerUp(e) {
                if (!pendingDrag) { dragging = false; ghost = null; return; }
                const pd = pendingDrag;
                pendingDrag = null;
                const wasDrag = dragging;
                dragging = false;
                ghost = null;
                if (wasDrag && pd.id) {
                        const td = hitCell(e.clientX, e.clientY);
                        if (td) {
                                const tDay = parseInt(td.getAttribute("data-day"));
                                const tSlot = parseInt(td.getAttribute("data-slot"));
                                const tRow = parseInt(td.getAttribute("data-row"));
                                const tKind = td.getAttribute("data-kind");
                                if (!isNaN(tDay) && !isNaN(tSlot) && !isNaN(tRow)) {
                                        if (tDay === pd.day && tSlot === pd.slot && tRow === pd.rowId) return;
                                        try { await applyMove(pd.id, tKind, tRow, tDay, tSlot); }
                                        catch (err) { flash("Ошибка: " + (err && err.message ? err.message : err)); }
                                }
                        }
                        return;
                }
                // Click without drag: select or click-to-move
                if (!pd.id) {
                        if (selectedEntry) {
                                try { await applyMove(selectedEntry.id, pd.kind, pd.rowId, pd.day, pd.slot); }
                                catch (err) { flash("Ошибка: " + (err && err.message ? err.message : err)); }
                                selectedEntry = null;
                        }
                        return;
                }
                if (selectedEntry && selectedEntry.id !== pd.id) {
                        try { await applyMove(selectedEntry.id, pd.kind, pd.rowId, pd.day, pd.slot); }
                        catch (err) { flash("Ошибка: " + (err && err.message ? err.message : err)); }
                        selectedEntry = null;
                } else {
                        selectedEntry = (selectedEntry && selectedEntry.id === pd.id) ? null : { id: pd.id };
                }
        }
        function onPointerCancel() { pendingDrag = null; dragging = false; ghost = null; startPos = null; }

        // Attach pointer listeners to window directly (more reliable in the Wails
        // webview than the <svelte:window> directive) and clean up on destroy.
        function attachDragListeners() {
                // Capture phase is important in the embedded Wails webview: a table
                // cell can consume pointerup, otherwise the toast is shown but the
                // actual drop handler is never reached reliably.
                document.addEventListener("pointermove", onPointerMove, true);
                document.addEventListener("pointerup", onPointerUp, true);
                document.addEventListener("pointercancel", onPointerCancel, true);
        }
        function detachDragListeners() {
                document.removeEventListener("pointermove", onPointerMove, true);
                document.removeEventListener("pointerup", onPointerUp, true);
                document.removeEventListener("pointercancel", onPointerCancel, true);
        }
        onMount(() => { attachDragListeners(); detectPreciseSolver(); return () => detachDragListeners(); });
        function cellAt(kind, id, day, slot) {
                const e = schedule.find((en) => {
                        let match;
                        if (kind === "class") match = en.class_id === id;
                        else if (kind === "teacher") match = en.teacher_id === id;
                        else match = en.room_id === id;
                        return match && en.day_of_week === day && en.timeslot === slot;
                });
                if (!e) return null;
                return {
                        id: e.id,
                        subject_id: e.subject_id,
                        teacher_id: e.teacher_id,
                        room_id: e.room_id,
                        conflict: conflictIDs.has(e.id),
                        label: subjName(subjects, e.subject_id) + " (" + teachName(teachers, e.teacher_id) + ") " + (rooms.find(r => r.id === e.room_id)?.name || "")
                };
        }

        async function exportJSON() {
                const snap = await ExportAll(activeSchoolID);
                await saveFile("school.json", JSON.stringify(snap, null, 2), "application/json", false);
        }
        async function importJSON(ev) {
                const text = await ev.target.files[0].text();
                await ImportAll(text);
                await loadSchools();
                await reloadRefs();
                flash("Импортировано");
        }
        async function exportCSV() {
                const csv = await ScheduleCSV(activeSchoolID, days, slots);
                await saveFile("schedule.csv", csv, "text/csv", false);
        }
        function hexToRgb(hex) {
                const h = String(hex).replace("#", "");
                const full = h.length === 3 ? h.split("").map(c => c + c).join("") : h;
                const n = parseInt(full, 16);
                return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
        }

        async function exportPDF() {
                try {
                const kind = exportMode === "school" ? "class" : exportMode;
                const list = entityRows(kind);
                if (!list.length) {
                        flash("PDF не создан: нет строк расписания для экспорта.");
                        return;
                }
                const daysN = pdfWeekdaysOnly ? Math.min(days, 5) : days;
                const land = orientation === "landscape";
                const doc = new jsPDF({ orientation: land ? "landscape" : "portrait", unit: "mm", format: pageSize.toLowerCase() });
                const pageW = doc.internal.pageSize.getWidth();
                const pageH = doc.internal.pageSize.getHeight();
                const margin = 8;
                const labelW = 22;
                const gridX = margin + labelW;
                const topY = margin + 10;
                const legendH = exportMode === "school" ? 16 : 0;
                const gridW = pageW - gridX - margin;
                const gridH = pageH - topY - margin - legendH;
                const colW = gridW / slots;
                const rowH = gridH / daysN;
                let first = true;
                for (const row of list) {
                        if (!first) doc.addPage(pageSize.toLowerCase(), land ? "landscape" : "portrait");
                        first = false;
                        doc.setFontSize(13);
                        doc.setTextColor(20, 20, 20);
                        doc.text(escapeHtml(row.label), margin, margin + 4);
                        doc.setFontSize(8);
                        for (let si = 0; si < slots; si++) {
                                const lbl = periodLabel(si);
                                doc.text("П" + (si + 1) + (lbl ? " " + lbl : ""), gridX + si * colW + 1, topY - 2);
                        }
                        for (let di = 0; di < daysN; di++) {
                                doc.text(dayName(di), margin, topY + (di + 0.5) * rowH + 2);
                                for (let si = 0; si < slots; si++) {
                                        const cell = cellAt(kind, row.id, di, si);
                                        const x = gridX + si * colW;
                                        const y = topY + di * rowH;
                                        if (cell) {
                                                const bg = cell.conflict ? "#b91c1c" : (pdfBW ? "#e5e7eb" : subjectColor(cell.subject_id));
                                                const [r, g, b] = hexToRgb(bg);
                                                doc.setFillColor(r, g, b);
                                                doc.rect(x, y, colW, rowH, "F");
                                                doc.setTextColor(20, 20, 20);
                                                doc.setFontSize(7);
                                                const txt = pdfShowTeacher
                                                        ? subjName(subjects, cell.subject_id) + " (" + teachName(teachers, cell.teacher_id) + ")"
                                                        : subjName(subjects, cell.subject_id);
                                                const lines = doc.splitTextToSize(txt, colW - 2);
                                                doc.text(lines.slice(0, 3), x + 1, y + 4);
                                        } else {
                                                doc.setDrawColor(200);
                                                doc.rect(x, y, colW, rowH);
                                        }
                                }
                        }
                }
                if (exportMode === "school") {
                        let lx = margin, ly = pageH - margin - 4;
                        doc.setFontSize(8);
                        for (const s of subjects) {
                                if (!schedule.some(e => e.subject_id === s.id)) continue;
                                const [r, g, b] = hexToRgb(pdfBW ? "#e5e7eb" : subjectColor(s.id));
                                doc.setFillColor(r, g, b);
                                doc.rect(lx, ly - 3, 4, 4, "F");
                                doc.setTextColor(20, 20, 20);
                                doc.text(escapeHtml(s.name), lx + 5, ly);
                                lx += 7 + doc.getTextWidth(escapeHtml(s.name)) + 4;
                                if (lx > pageW - 30) { lx = margin; ly -= 5; }
                        }
                }
                // datauristring is unreliable in some WebKit/Wails builds and can
                // yield a syntactically valid but empty PDF. Encode the PDF bytes
                // directly instead.
                const bytes = new Uint8Array(doc.output("arraybuffer"));
                let binary = "";
                const chunk = 0x8000;
                for (let offset = 0; offset < bytes.length; offset += chunk) {
                        binary += String.fromCharCode(...bytes.subarray(offset, offset + chunk));
                }
                const base64 = btoa(binary);
                if (!base64) throw new Error("PDF не содержит данных");
                const defName = "Расписание_" + fileSlug(pdfSchoolName()) + "_" + new Date().toISOString().slice(0, 10) + ".pdf";
                try {
                        await saveFile(defName, base64, "application/pdf", true);
                } catch (err) {
                        flash("Ошибка сохранения PDF: " + (err && err.message ? err.message : err));
                }
                } catch (e) {
                        flash("Ошибка генерации PDF: " + (e && e.message ? e.message : e));
                }
        }
        function entityRows(kind) {
                if (kind === "teacher") return teachers.map((t) => ({ id: t.id, label: t.name }));
                if (kind === "room") return rooms.map((r) => ({ id: r.id, label: r.name }));
                return classes.map((c) => ({ id: c.id, label: c.name }));
        }
        function exportBlock(row, kind, pageBreak) {
                const daysN = pdfWeekdaysOnly ? Math.min(days, 5) : days;
                let h = pageBreak ? '<div class="page">' : "<div>";
                h += '<div class="doc-header"><h2>' + escapeHtml(row.label) + "</h2>";
                h += '<div class="doc-meta">' + escapeHtml(pdfSchoolName()) + " · " + exportTitle() + " · " + new Date().toLocaleDateString("ru-RU") + "</div></div>";
                h += "<table><thead><tr><th>День</th>";
                for (let si = 0; si < slots; si++) {
                        const lbl = periodLabel(si);
                        h += "<th>П" + (si + 1) + (lbl ? " " + lbl : "") + "</th>";
                }
                h += "</tr></thead><tbody>";
                for (let di = 0; di < daysN; di++) {
                        h += "<tr><td class=\"day\">" + dayName(di) + "</td>";
                        for (let si = 0; si < slots; si++) {
                                const cell = cellAt(kind, row.id, di, si);
                                if (!cell) { h += "<td></td>"; continue; }
                                const bg = cell.conflict ? "#b91c1c" : (pdfBW ? "#e5e7eb" : subjectColor(cell.subject_id));
                                const fg = cell.conflict ? "#ffffff" : "#000000";
                                h += '<td style="background:' + bg + ";color:" + fg + '">' + escapeHtml(cellLabel(cell)) + "</td>";
                        }
                        h += "</tr>";
                }
                return h + "</tbody></table></div>";
        }
        function subjectColor(sid) {
                const palette = ["#dbeafe","#dcfce7","#fef9c3","#fae8ff","#ffedd5","#cffafe","#fecaca","#e0e7ff","#d1fae5","#fee2e2","#fef3c7","#ede9fe","#ccfbf1","#fce7f3"];
                return palette[Math.abs((sid || 0)) % palette.length];
        }
        function cellLabel(cell) {
                const parts = [subjName(subjects, cell.subject_id)];
                if (pdfShowTeacher) parts.push("(" + teachName(teachers, cell.teacher_id) + ")");
                if (pdfShowRoom) {
                        const rn = rooms.find(r => r.id === cell.room_id)?.name;
                        if (rn) parts.push(rn);
                }
                return parts.filter(Boolean).join(" ");
        }
        function pdfSchoolName() {
                return (schools.find(s => s.id === activeSchoolID)?.name) || "Школа";
        }
        function exportTitle() {
                const m = { school: "вся школа", class: "по классам", teacher: "по учителям", room: "по кабинетам" };
                return m[exportMode] || "расписание";
        }
        function fileSlug(s) {
                return String(s).replace(/[\\/:*?"<>|]/g, "_").replace(/\s+/g, "_");
        }
        function buildLegend() {
                const used = new Set(schedule.map(e => e.subject_id));
                let h = '<div class="legend"><h3>Легенда</h3><div class="legend-row">';
                for (const s of subjects) {
                        if (!used.has(s.id)) continue;
                        const bg = pdfBW ? "#e5e7eb" : subjectColor(s.id);
                        h += '<span class="legend-item"><span class="sw" style="background:' + bg + '"></span>' + escapeHtml(s.name) + "</span>";
                }
                h += "</div>";
                h += '<div class="legend-note">' + (pdfBW ? "Ч/Б: серый — любой предмет; красный — конфликт." : "Красный — конфликт расписания.") + "</div></div>";
                return h;
        }
        function buildConflictList() {
                if (conflictIDs.size === 0) return "";
                let h = '<div class="conflicts"><h3>Конфликты</h3><ul>';
                for (const e of schedule) {
                        if (!conflictIDs.has(e.id)) continue;
                        h += "<li>" + escapeHtml(cellLabel({ subject_id: e.subject_id, teacher_id: e.teacher_id, room_id: e.room_id })) + " — " + dayName(e.day_of_week) + " П" + (e.timeslot + 1) + "</li>";
                }
                return h + "</ul></div>";
        }
        function buildExportHTML() {
                const kind = exportMode === "school" ? "class" : exportMode;
                const list = entityRows(kind);
                let body = "";
                if (exportMode === "school") {
                        for (const row of list) body += exportBlock(row, kind, false);
                        body += buildLegend();
                        body += buildConflictList();
                } else {
                        for (const row of list) body += exportBlock(row, kind, true);
                }
                const cs = exportMode === "school"
                        ? "table{border-collapse:collapse;width:100%}td,th{border:1px solid #999;padding:1px;font-size:8px}.day{font-size:8px}"
                        : "table{border-collapse:collapse;width:100%}td,th{border:1px solid #999;padding:3px;font-size:11px}.day{font-size:11px}";
                const title = "Расписание_" + fileSlug(pdfSchoolName()) + "_" + new Date().toISOString().slice(0, 10);
                return "<html><head><meta charset=\"utf-8\"><title>" + escapeHtml(title) + "</title><" + "style>"
                        + "@page{size:" + pageSize + " " + orientation + ";margin:8mm}"
                        + "body{font-family:Arial,sans-serif;color:#000}.page{break-after:page}" + cs
                        + ".doc-header{margin-bottom:4px}.doc-header h2{margin:0;font-size:14px}.doc-meta{color:#444;font-size:10px;margin-bottom:2px}"
                        + "td{white-space:nowrap;overflow:hidden}"
                        + ".legend{margin-top:8px}.legend h3{font-size:11px;margin:0 0 2px}.legend-row{display:flex;flex-wrap:wrap;gap:8px}.legend-item{display:inline-flex;align-items:center;gap:3px;font-size:9px}.sw{width:10px;height:10px;display:inline-block;border:1px solid #999}.legend-note{font-size:8px;color:#555;margin-top:2px}"
                        + ".conflicts{margin-top:8px;font-size:9px}.conflicts h3{font-size:11px;margin:0 0 2px}.conflicts li{color:#b91c1c}"
                        + "<" + "/style></head><body>" + body + "</body></html>";
        }
        function escapeHtml(s) {
                return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
        }
        let saveModal = null; // { filename, content, mime, isBase64 }
        async function saveFile(filename, content, mime, isBase64) {
                saveModal = { filename, content, mime, isBase64 };
        }
        async function confirmSave() {
                if (!saveModal) return;
                const name = saveModal.filename || "export";
                const b64 = saveModal.isBase64 ? saveModal.content : btoa(unescape(encodeURIComponent(saveModal.content)));
                try {
                        const path = await SaveExport(name, b64);
                        flash("Сохранено: " + path);
                } catch (e) {
                        flash("Ошибка сохранения: " + (e && e.message ? e.message : e));
                }
                saveModal = null;
        }
        function cancelSave() { saveModal = null; }
        async function downloadRefsCSV(entity) {
                const csv = await ExportRefsCSV(activeSchoolID, entity);
                await saveFile(entity + ".csv", csv, "text/csv", false);
        }
        async function importRefsCSV(entity, e) {
                const file = e.target.files && e.target.files[0];
                if (!file) return;
                const text = await file.text();
                try {
                        const n = await ImportRefsCSV(activeSchoolID, entity, text);
                        await reloadRefs();
                        if (entity === "periods") await loadSettings();
                        flash("Импортировано строк: " + n);
                } catch (err) {
                        flash("Ошибка импорта: " + err.message);
                } finally {
                        e.target.value = "";
                }
        }

        async function seedDemo() {
                if (!activeSchoolID) { await createSchool(); }
                const sid = activeSchoolID;
                const T = await CreateTeacher({ school_id: sid, name: "Иванов", short_name: "Ив", max_hours_per_week: 30 });
                const T2 = await CreateTeacher({ school_id: sid, name: "Петрова", short_name: "Пт", max_hours_per_week: 30 });
                const S1 = await CreateSubject({ school_id: sid, name: "Математика", short_name: "М", requires_room_type: "any" });
                const S2 = await CreateSubject({ school_id: sid, name: "Физика", short_name: "Ф", requires_room_type: "any" });
                const C1 = await CreateClass({ school_id: sid, name: "10А", grade: 10, student_count: 25 });
                const C2 = await CreateClass({ school_id: sid, name: "11Б", grade: 11, student_count: 22 });
                const R1 = await CreateRoom({ school_id: sid, name: "301", capacity: 30, room_type: "any" });
                const R2 = await CreateRoom({ school_id: sid, name: "302", capacity: 30, room_type: "any" });
                await CreateLesson({ school_id: sid, class_id: C1.id, subject_id: S1.id, teacher_id: T.id, hours_per_week: 5, min_gap_days: 1, can_split: false, preferred_rooms: "[]" });
                await CreateLesson({ school_id: sid, class_id: C1.id, subject_id: S2.id, teacher_id: T2.id, hours_per_week: 3, min_gap_days: 1, can_split: false, preferred_rooms: "[]" });
                await CreateLesson({ school_id: sid, class_id: C2.id, subject_id: S1.id, teacher_id: T.id, hours_per_week: 4, min_gap_days: 1, can_split: false, preferred_rooms: "[]" });
                await CreateLesson({ school_id: sid, class_id: C2.id, subject_id: S2.id, teacher_id: T2.id, hours_per_week: 3, min_gap_days: 1, can_split: false, preferred_rooms: "[]" });
                await reloadRefs();
                flash("Демо-данные загружены: 2 учителя, 2 предмета, 2 класса, 2 кабинета, 4 урока");
        }

        async function seedDemoLarge() {
                if (!activeSchoolID) { await createSchool(); }
                const sid = activeSchoolID;
                const subs = ["Математика","Русский язык","Литература","Английский язык","История","Обществознание","Биология","География","Физика","Химия","Информатика","Физкультура","ИЗО","Технология","Музыка"];
                const subjIDs = {};
                for (const nm of subs) {
                        const s = await CreateSubject({ school_id: sid, name: nm, short_name: nm.slice(0, 4), requires_room_type: "any" });
                        subjIDs[nm] = s.id;
                }
                const surnames = ["Иванов","Петров","Сидоров","Смирнов","Кузнецов","Попов","Соколов","Лебедев","Козлов","Новиков","Морозов","Волков","Васильев","Зайцев","Павлов","Семёнов","Голубев","Виноградов","Богданов","Воробьёв","Фёдоров","Михайлов","Беляев","Тарасов","Орлов","Комаров","Киселёв","Барсуков","Макаров","Никитин","Захаров","Сорокин","Егоров","Титов","Осипов","Киреев","Громов","Снегирёв","Веселов","Яковлев"];
                const initials = ["А.А.","Б.Б.","В.В.","Г.Г.","Д.Д.","Е.Е.","И.И.","К.К.","Л.Л.","М.М.","Н.Н.","О.О.","П.П.","Р.Р.","С.С.","Т.Т."];
                const teachIDs = [];
                for (let i = 0; i < surnames.length; i++) {
                        const t = await CreateTeacher({ school_id: sid, name: surnames[i] + " " + initials[i % initials.length], short_name: surnames[i].slice(0, 3), max_hours_per_week: 40 });
                        teachIDs.push(t.id);
                }
                const letters = ["А", "Б", "В", "Г", "Д"];
                const classes = [];
                let cnt = 0;
                for (let g = 5; g <= 11 && cnt < 35; g++) {
                        for (const L of letters) {
                                if (cnt >= 35) break;
                                const c = await CreateClass({ school_id: sid, name: g + L, grade: g, student_count: 25, subgroup_of: null });
                                classes.push(c); cnt++;
                        }
                }
                let ti = 0;
                for (let ci = 0; ci < classes.length; ci++) {
                        const c = classes[ci];
                        const n = 6 + (c.grade % 4);  // 6–9 lessons per class based on grade
                        for (let k = 0; k < n; k++) {
                                const subj = subs[(c.grade * 3 + k) % subs.length];
                                const teacher = teachIDs[ti % teachIDs.length]; ti++;
                                const hours = 2 + ((c.grade + k) % 3);  // 2–4 hours per week
                                await CreateLesson({ school_id: sid, class_id: c.id, subject_id: subjIDs[subj], teacher_id: teacher, hours_per_week: hours, min_gap_days: 1, can_split: false, preferred_rooms: "[]" });
                        }
                }
                await reloadRefs();
                flash("Демо (35 классов) загружено: " + classes.length + " классов");
        }

        function className(list, id) { const x = list.find(c => c.id === id); return x ? x.name : "?"; }
        function subjName(list, id) { const x = list.find(s => s.id === id); return x ? x.name : "?"; }
        function teachName(list, id) { const x = list.find(t => t.id === id); return x ? (x.short_name || x.name) : "?"; }
        function dayName(d) { return ["Пн","Вт","Ср","Чт","Пт","Сб","Вс"][d] || ("Д" + (d + 1)); }
        function sI(i) { return i + 1; }
        function periodLabel(si) { const p = bellPeriods[si]; return p && p.start ? p.start + "–" + p.end : ""; }
        function slotLabel(si) {
                const lbl = periodLabel(si);
                return "П" + (si + 1) + (lbl ? " " + lbl : "");
        }

        // -- Constraint UI helpers --
        const CONSTRAINT_FIELDS = {
                teacher_unavailable:    { day: true,  slots: true,  value: false, valueLabel: "" },
                class_unavailable:      { day: true,  slots: true,  value: false, valueLabel: "" },
                room_unavailable:       { day: true,  slots: true,  value: false, valueLabel: "" },
                max_consecutive:        { day: false, slots: false, value: true,  valueLabel: "Макс. подряд" },
                lunch_break:            { day: false, slots: true,  value: false, valueLabel: "" },
                max_lessons_per_day:    { day: false, slots: false, value: true,  valueLabel: "Макс. в день" },
                min_lessons_per_day:    { day: false, slots: false, value: true,  valueLabel: "Мин. в день" },
                prefer_morning:         { day: false, slots: true,  value: false, valueLabel: "" },
                max_gaps:               { day: false, slots: false, value: true,  valueLabel: "Макс. окон" },
        };
        const CONSTRAINT_LABELS = {
                teacher_unavailable:    "Учитель недоступен",
                class_unavailable:      "Класс недоступен",
                room_unavailable:       "Кабинет недоступен",
                max_consecutive:        "Макс. подряд уроков",
                lunch_break:            "Обеденный перерыв",
                max_lessons_per_day:    "Макс. уроков в день",
                min_lessons_per_day:    "Мин. уроков в день",
                prefer_morning:         "Желательно утро",
                max_gaps:               "Макс. окон",
        };
        function constraintTypeLabel(t) { return CONSTRAINT_LABELS[t] || t; }
        function constraintFields(t) { return CONSTRAINT_FIELDS[t] || { day: false, slots: false, value: false, valueLabel: "" }; }
        function constraintEntityLabel(c) {
                if (c.entity_type === "school") return "вся школа";
                const list = c.entity_type === "teacher" ? teachers : c.entity_type === "class" ? classes : rooms;
                const item = list.find(x => x.id === c.entity_id);
                return item ? item.name : "#" + c.entity_id;
        }
        function constraintDetail(c) {
                const f = constraintFields(c.type);
                const parts = [];
                if (f.day && c.day_of_week != null) parts.push(dayName(c.day_of_week));
                if (f.slots) {
                        if (c.timeslot_start != null && c.timeslot_end != null) {
                                parts.push("П" + (c.timeslot_start + 1) + "–П" + (c.timeslot_end + 1));
                        } else if (c.timeslot_start != null) {
                                parts.push("с П" + (c.timeslot_start + 1));
                        } else if (c.timeslot_end != null) {
                                parts.push("по П" + (c.timeslot_end + 1));
                        }
                }
                if (f.value) parts.push(String(c.weight));
                return parts.join(", ");
        }
        function constraintSummary(c) {
                const detail = constraintDetail(c);
                return constraintTypeLabel(c.type) + " · " + constraintEntityLabel(c) + (detail ? " (" + detail + ")" : "") + " · " + (c.is_hard ? "🔒 жёсткое" : "📝 мягкое");
        }

        // -- DnD helper for compact labels in flash --
        function cellLabelShort(e) {
                if (!e) return "?";
                return subjName(subjects, e.subject_id) + " (" + teachName(teachers, e.teacher_id) + ")";
        }

        loadSchools();
</script>

<div class="app">
        <aside class="sidebar">
                <div class="brand">📅 <span>Timetable</span></div>
                <nav class="nav">
                        <button class:active={tab === "refs"} on:click={() => tab = "refs"}><span class="ico">📚</span>Справочники</button>
                        <button class:active={tab === "lessons"} on:click={() => tab = "lessons"}><span class="ico">📝</span>Учебный план</button>
                        <button class:active={tab === "constraints"} on:click={() => tab = "constraints"}><span class="ico">⚠️</span>Ограничения</button>
                        <button class:active={tab === "settings"} on:click={() => tab = "settings"}><span class="ico">⚙️</span>Настройки</button>
                        <button class:active={tab === "schedule"} on:click={() => tab = "schedule"}><span class="ico">🗓️</span>Расписание</button>
                </nav>
                <div class="side-foot">
                        <button class="ghost" on:click={seedDemo}>⚡ Демо-данные</button>
                        <button class="ghost" on:click={seedDemoLarge}>⚡ Демо 35 классов</button>
                </div>
        </aside>

        <div class="content">
                <header class="topbar">
                        <div class="school">
                                <select bind:value={activeSchoolID} on:change={async () => { await reloadRefs(); await loadSettings(); }}>
                                        {#each schools as sc}<option value={sc.id}>{sc.name}</option>{/each}
                                </select>
                                {#if schools.length === 0}<span class="muted">нет школ</span>{/if}
                                <input class="school-new" bind:value={newSchoolName} placeholder="Новая школа" />
                                <button class="primary sm" on:click={createSchool}>+ Школа</button>
                                {#if schools.length > 1}
                                        <button class="danger sm" on:click={async () => { if (await confirmAction('Удалить школу «' + (schools.find(s => s.id === activeSchoolID)?.name || '') + '» и все данные?')) { await DeleteSchool(activeSchoolID); activeSchoolID = 0; await loadSchools(); flash('Школа удалена'); } }}>✕</button>
                                {/if}
                        </div>
                        {#if msg}<div class="toast">{msg}</div>{/if}
                        <span class="ver">v{APP_VERSION}</span>
                </header>

                <main>
                        {#if tab === "refs"}
                                <div class="cards">
                                        <section class="card">
                                                <div class="card-head">
                                                        <h2>Учителя</h2>
                                                        <div class="csvbar">
                                                                <button class="mini" on:click={() => downloadRefsCSV('teachers')}>⬇ CSV</button>
                                                                <label class="mini file">⬆<input type="file" accept=".csv" on:change={(e) => importRefsCSV('teachers', e)} /></label>
                                                        </div>
                                                </div>
                                                <div class="row">
                                                        <input bind:value={t.name} placeholder="Имя" />
                                                        <input bind:value={t.short_name} placeholder="Кратко" />
                                                        <input type="number" bind:value={t.max_hours_per_week} title="Часов/нед" />
                                                        <button class="primary" on:click={addTeacher}>+</button>
                                                </div>
                                                <ul class="list">{#each teachers as x}<li>{x.name} <small>({x.short_name})</small> <span class="muted">— {x.max_hours_per_week}ч/нед</span><button class="danger sm" on:click={() => removeTeacher(x.id)}>✕</button></li>{/each}</ul>
                                        </section>

                                        <section class="card">
                                                <div class="card-head">
                                                        <h2>Предметы</h2>
                                                        <div class="csvbar">
                                                                <button class="mini" on:click={() => downloadRefsCSV('subjects')}>⬇ CSV</button>
                                                                <label class="mini file">⬆<input type="file" accept=".csv" on:change={(e) => importRefsCSV('subjects', e)} /></label>
                                                        </div>
                                                </div>
                                                <div class="row">
                                                        <input bind:value={s.name} placeholder="Название" />
                                                        <input bind:value={s.short_name} placeholder="Кратко" />
                                                        <input bind:value={s.requires_room_type} placeholder="Тип каб." />
                                                        <button class="primary" on:click={addSubject}>+</button>
                                                </div>
                                                <ul class="list">{#each subjects as x}<li>{x.name} <small>({x.short_name})</small><button class="danger sm" on:click={() => removeSubject(x.id)}>✕</button></li>{/each}</ul>
                                        </section>

                                        <section class="card">
                                                <div class="card-head">
                                                        <h2>Классы</h2>
                                                        <div class="csvbar">
                                                                <button class="mini" on:click={() => downloadRefsCSV('classes')}>⬇ CSV</button>
                                                                <label class="mini file">⬆<input type="file" accept=".csv" on:change={(e) => importRefsCSV('classes', e)} /></label>
                                                        </div>
                                                </div>
                                                <div class="row">
                                                        <input bind:value={c.name} placeholder="10А" />
                                                        <input type="number" bind:value={c.grade} placeholder="Класс" />
                                                        <input type="number" bind:value={c.student_count} placeholder="Уч-ся" />
                                                        <select bind:value={c.subgroup_of}><option value={null}>— целый класс —</option>{#each classes as x}<option value={x.id}>{x.name} (подгруппа)</option>{/each}</select>
                                                        <button class="primary" on:click={addClass}>+</button>
                                                </div>
                                                <ul class="list">{#each classes as x}<li>{x.name} <span class="muted">— {x.student_count} чел.</span>{x.subgroup_of ? " · подгруппа" : ""}<button class="danger sm" on:click={() => removeClass(x.id)}>✕</button></li>{/each}</ul>
                                        </section>

                                        <section class="card">
                                                <div class="card-head">
                                                        <h2>Кабинеты</h2>
                                                        <div class="csvbar">
                                                                <button class="mini" on:click={() => downloadRefsCSV('rooms')}>⬇ CSV</button>
                                                                <label class="mini file">⬆<input type="file" accept=".csv" on:change={(e) => importRefsCSV('rooms', e)} /></label>
                                                        </div>
                                                </div>
                                                <div class="row">
                                                        <input bind:value={r.name} placeholder="301" />
                                                        <input type="number" bind:value={r.capacity} placeholder="Мест" />
                                                        <input bind:value={r.room_type} placeholder="Тип" />
                                                        <button class="primary" on:click={addRoom}>+</button>
                                                </div>
                                                <ul class="list">{#each rooms as x}<li>{x.name} <span class="muted">— {x.capacity} мест</span><button class="danger sm" on:click={() => removeRoom(x.id)}>✕</button></li>{/each}</ul>
                                        </section>
                                </div>
                        {:else if tab === "lessons"}
                                <section class="card">
                                        <div class="card-head">
                                                <h2>Учебный план (уроки)</h2>
                                                <div class="csvbar">
                                                        <button class="mini" on:click={() => downloadRefsCSV('lessons')}>⬇ CSV</button>
                                                        <label class="mini file">⬆<input type="file" accept=".csv" on:change={(e) => importRefsCSV('lessons', e)} /></label>
                                                </div>
                                        </div>
                                        <div class="lesson-form">
                                                <select bind:value={curClass}><option value={0}>Выберите класс…</option>{#each classes as x}<option value={x.id}>{x.name}</option>{/each}</select>
                                        </div>
                                        {#if curClass}
                                                {@const cl = lessons.filter((x) => x.class_id === curClass)}
                                                <table class="data">
                                                        <thead><tr><th>Предмет</th><th>Учитель</th><th>Ч/нед</th><th></th></tr></thead>
                                                        <tbody>
                                                                {#each cl as x}
                                                                        <tr>
                                                                                <td>{subjName(subjects, x.subject_id)}</td>
                                                                                <td><select bind:value={x.teacher_id} on:change={() => updateLesson(x)}>{#each teachers as t}<option value={t.id}>{t.name}</option>{/each}</select></td>
                                                                                <td><input type="number" min="1" max="40" bind:value={x.hours_per_week} on:change={() => updateLesson(x)} /></td>
                                                                                <td class="act"><button class="danger sm" on:click={() => removeLesson(x.id)}>✕</button></td>
                                                                        </tr>
                                                                {/each}
                                                                <tr class="addrow">
                                                                        <td><select bind:value={l.subject_id}><option value={0}>Предмет</option>{#each subjects as s}<option value={s.id}>{s.name}</option>{/each}</select></td>
                                                                        <td><select bind:value={l.teacher_id}><option value={0}>Учитель</option>{#each teachers as t}<option value={t.id}>{t.name}</option>{/each}</select></td>
                                                                        <td><input type="number" min="1" max="40" bind:value={l.hours_per_week} placeholder="Ч/нед" /></td>
                                                                        <td class="act"><button class="primary sm" on:click={addLessonForClass}>+ Добавить</button></td>
                                                                </tr>
                                                        </tbody>
                                                </table>
                                                {#if cl.length === 0}<p class="muted">У этого класса пока нет уроков. Добавьте предмет и учителя выше.</p>{/if}
                                        {:else}
                                                <p class="muted">Выберите класс, чтобы увидеть и редактировать его учебный план (предмет + учитель + часы в неделю).</p>
                                        {/if}
                                </section>
                        {:else if tab === "constraints"}
                                <section class="card">
                                        <div class="card-head"><h2>Ограничения</h2></div>
                                        <div class="lesson-form constraints-form">
                                                <label class="field">
                                                        <span class="field-label">Тип</span>
                                                        <select bind:value={con.type} on:change={resetConstraintFields}>
                                                                {#each Object.entries(CONSTRAINT_LABELS) as [key, label]}
                                                                        <option value={key}>{label}</option>
                                                                {/each}
                                                        </select>
                                                </label>
                                                <label class="field">
                                                        <span class="field-label">К кому</span>
                                                        <select bind:value={con.entity_type}>
                                                                <option value="teacher">Учитель</option>
                                                                <option value="class">Класс</option>
                                                                <option value="room">Кабинет</option>
                                                                <option value="school">Школа</option>
                                                        </select>
                                                </label>
                                                {#if con.entity_type === "school"}
                                                        <span class="muted">вся школа</span>
                                                {:else}
                                                        <label class="field">
                                                                <span class="field-label">Кто</span>
                                                                <select bind:value={con.entity_id}>
                                                                        <option value={0}>— выберите —</option>
                                                                        {#if con.entity_type === "teacher"}
                                                                                {#each teachers as x}<option value={x.id}>{x.name}</option>{/each}
                                                                        {:else if con.entity_type === "class"}
                                                                                {#each classes as x}<option value={x.id}>{x.name}</option>{/each}
                                                                        {:else if con.entity_type === "room"}
                                                                                {#each rooms as x}<option value={x.id}>{x.name}</option>{/each}
                                                                        {/if}
                                                                </select>
                                                        </label>
                                                {/if}
                                                {#if constraintFields(con.type).day}
                                                        <label class="field">
                                                                <span class="field-label">День</span>
                                                                <select bind:value={con.day_of_week}>
                                                                        <option value={null}>— любой день —</option>
                                                                        {#each Array(days) as _, di}
                                                                                <option value={di}>{dayName(di)}</option>
                                                                        {/each}
                                                                </select>
                                                        </label>
                                                {/if}
                                                {#if constraintFields(con.type).slots}
                                                        <label class="field">
                                                                <span class="field-label">Слот с</span>
                                                                <select bind:value={con.timeslot_start}>
                                                                        <option value={null}>— любой —</option>
                                                                        {#each Array(slots) as _, si}
                                                                                <option value={si}>{slotLabel(si)}</option>
                                                                        {/each}
                                                                </select>
                                                        </label>
                                                        <label class="field">
                                                                <span class="field-label">Слот по</span>
                                                                <select bind:value={con.timeslot_end}>
                                                                        <option value={null}>— любой —</option>
                                                                        {#each Array(slots) as _, si}
                                                                                <option value={si}>{slotLabel(si)}</option>
                                                                        {/each}
                                                                </select>
                                                        </label>
                                                {/if}
                                                {#if constraintFields(con.type).value}
                                                        <label class="field">
                                                                <span class="field-label">{constraintFields(con.type).valueLabel}</span>
                                                                <input type="number" min="0" max="20" bind:value={con.weight} />
                                                        </label>
                                                {/if}
                                                <label class="chk"><input type="checkbox" bind:checked={con.is_hard} /> 🔒 жёсткое</label>
                                                <button class="primary" on:click={addConstraint}>+ Добавить</button>
                                        </div>
                                        <p class="hint">Поля «день», «слот с/по» показываются только когда они нужны для выбранного типа. «любой день» или «любой слот» = без ограничения по этому параметру.</p>
                                        <ul class="list constraints-list">
                                                {#each constraints as x}
                                                        <li>
                                                                <span class="con-summary">{constraintSummary(x)}</span>
                                                                <button class="danger sm" on:click={() => removeConstraint(x.id)}>✕</button>
                                                        </li>
                                                {/each}
                                        </ul>
                                </section>
                        {:else if tab === "settings"}
                                <section class="card">
                                        <div class="card-head"><h2>Настройки школы</h2></div>
                                        <div class="row">
                                                <label>Учебных дней: <input type="number" min="1" max="7" bind:value={days} /></label>
                                                <label>Уроков в день: <input type="number" min="1" max="14" bind:value={slots} on:change={onSlotsChange} /></label>
                                                <button class="primary" on:click={saveSettings}>Сохранить</button>
                                                <span class="csvbar">
                                                        <button class="mini" on:click={() => downloadRefsCSV('periods')}>⬇ CSV звонков</button>
                                                        <label class="mini file">⬆<input type="file" accept=".csv" on:change={(e) => importRefsCSV('periods', e)} /></label>
                                                </span>
                                        </div>
                                        <h3>Расписание звонков</h3>
                                        <table class="data">
                                                <thead><tr><th>Урок</th><th>Начало</th><th>Конец</th></tr></thead>
                                                <tbody>
                                                        {#each Array(slots) as _, si}
                                                                <tr>
                                                                        <td>П{si + 1}</td>
                                                                        <td><input type="time" bind:value={bellPeriods[si].start} /></td>
                                                                        <td><input type="time" bind:value={bellPeriods[si].end} /></td>
                                                                </tr>
                                                        {/each}
                                                </tbody>
                                        </table>
                                        <p class="hint">Время отображается в шапке расписания и в PDF. Хранится в базе (SQLite).</p>
                                </section>
                        {:else if tab === "schedule"}
                                <section class="card">
                                        <div class="gen-bar">
                                                <label>Дней: <input class="num" type="number" bind:value={days} /></label>
                                                <label>Слотов: <input class="num" type="number" bind:value={slots} /></label>
                                                <button class="primary" on:click={generate}>⚙ Сгенерировать</button>
                                                <button on:click={undo}>↶ Отменить</button>
                                                <label class="chk" class:warn={!hasPreciseSolver} title={hasPreciseSolver ? "OR-Tools CP-SAT доступен в этой сборке" : "OR-Tools CP-SAT НЕ скомпилирован в эту сборку — будет использован быстрый эвристический решатель"}><input type="checkbox" bind:checked={usePrecise} /> точный CP-SAT (OR-Tools){#if !hasPreciseSolver} <span class="badge-warn">недоступно</span>{/if}</label>
                                                <span class="sep"></span>
                                                <label>Вид:
                                                        <select bind:value={viewMode}>
                                                                <option value="class">по классам</option>
                                                                <option value="teacher">по учителям</option>
                                                                <option value="room">по кабинетам</option>
                                                        </select>
                                                </label>
                                                <label class="chk"><input type="checkbox" bind:checked={compact} /> компактный</label>
                                                {#if viewMode === "class" && rows.length > classesPerPage}
                                                        <span class="pager">
                                                                <button class="sm" on:click={() => classPage = Math.max(0, classPage - 1)} disabled={classPage === 0}>‹</button>
                                                                <span>{classPage + 1}/{totalClassPages}</span>
                                                                <button class="sm" on:click={() => classPage = Math.min(totalClassPages - 1, classPage + 1)} disabled={classPage >= totalClassPages - 1}>›</button>
                                                        </span>
                                                {/if}
                                                <span class="sep"></span>
                                                <label>Экспорт:
                                                        <select bind:value={exportMode}>
                                                                <option value="school">вся школа (плакат)</option>
                                                                <option value="class">по классам (отд. стр.)</option>
                                                                <option value="teacher">по учителям</option>
                                                                <option value="room">по кабинетам</option>
                                                        </select>
                                                </label>
                                                <label>Стр.:
                                                        <select bind:value={pageSize}>
                                                                <option value="A0">A0</option><option value="A1">A1</option><option value="A2">A2</option><option value="A3">A3</option><option value="A4">A4</option>
                                                        </select>
                                                </label>
                                                <label>Ориент.:
                                                        <select bind:value={orientation}>
                                                                <option value="landscape">альбомн.</option>
                                                                <option value="portrait">книжн.</option>
                                                        </select>
                                                </label>
                                                <label class="chk"><input type="checkbox" bind:checked={pdfShowTeacher} /> учителя</label>
                                                <label class="chk"><input type="checkbox" bind:checked={pdfShowRoom} /> кабинеты</label>
                                                <label class="chk"><input type="checkbox" bind:checked={pdfWeekdaysOnly} /> будни</label>
                                                <label class="chk"><input type="checkbox" bind:checked={pdfBW} /> ч/б</label>
                                                <button on:click={exportPDF}>⬇ PDF</button>
                                                <button on:click={exportCSV}>⬇ CSV</button>
                                                <button on:click={exportJSON}>⬇ JSON</button>
                                                <label class="file">⬆ JSON<input type="file" accept="application/json" on:change={importJSON} /></label>
                                        </div>
                                        {#if genResult}<p class="status">Размещено <b>{genResult.placed}/{genResult.total}</b> · мягких нарушений: <b>{genResult.violations}</b></p>{/if}
                                        <p class="hint">Чтобы переместить урок: зажмите и перетащите ячейку в другую (drag) либо кликните урок, затем кликните целевую ячейку. Повторный клик по выделенному снимает выделение. ✕ в ячейке — удалить.</p>
                                        {#if schedule.length === 0}
                                                <p class="empty">Расписание пусто. Добавьте уроки и нажмите «Сгенерировать».</p>
                                        {:else}
                                                <div class="grid-scroll">
                                                        {#each visibleRows as row}
                                                                <div class="class-block" class:compact>
                                                                        <h3>{row.label}</h3>
                                                                        <table class:compact>
                                                                                <thead><tr><th>День</th>{#each Array(slots) as _, si}<th>П{si + 1}{periodLabel(si) ? " " + periodLabel(si) : ""}</th>{/each}</tr></thead>
                                                                                <tbody>
                                                                                        {#each Array(days) as _, di}
                                                                                                <tr><td class="day">{dayName(di)}</td>{#each Array(slots) as _, si}{@const cell = cellAt(viewMode, row.id, di, si)}<td
                                                                                                        class:filled={!!cell}
                                                                                                        class:conflict={!!cell && cell.conflict}
                                                                                                        style={cell ? 'background:' + subjectColor(cell.subject_id) : ''}
                                                                                                        class:selected={selectedEntry && cell && selectedEntry.id === cell.id}
                                                                                                        data-cell
                                                                                                        data-day={di}
                                                                                                        data-slot={si}
                                                                                                        data-row={row.id}
                                                                                                        data-kind={viewMode}
                                                                                                        on:pointerdown={(e) => onPointerDown(e, cell, viewMode, row.id, di, si)}>{#if cell}{cell.label}<button class="cell-x" title="Удалить" on:click={(e) => { e.stopPropagation(); removeEntry(cell.id); }}>✕</button>{:else}{/if}</td>{/each}</tr>
                                                                                        {/each}
                                                                                </tbody>
                                                                        </table>
                                                                </div>
                                                        {/each}
                                                </div>
                                                {#if ghost}<div class="drag-ghost" style="left:{ghost.x}px; top:{ghost.y}px;">{ghost.label}</div>{/if}
                                                {#if report.conflicts.length || report.unplaced.length || report.overloads.length}
                                                        <div class="report">
                                                                <h3>Отчёт по конфликтам</h3>
                                                                {#if report.conflicts.length}
                                                                        <div class="rep-sec"><b>Накладки ({report.conflicts.length}):</b>
                                                                                <ul>{#each report.conflicts as c}<li>{dayName(c.day)} П{c.slot + 1}: {c.type} — {#each c.items as it, i}{it.subject} ({it.who}){#if i < c.items.length - 1}, {/if}{/each}</li>{/each}</ul>
                                                                        </div>
                                                                {/if}
                                                                {#if report.unplaced.length}
                                                                        <div class="rep-sec"><b>Не расставлено уроков ({report.unplaced.length}):</b>
                                                                                <ul>{#each report.unplaced as u}<li>{u.subject} · {u.cls} — нужно {u.need}ч, расставлено {u.got}ч</li>{/each}</ul>
                                                                        </div>
                                                                {/if}
                                                                {#if report.overloads.length}
                                                                        <div class="rep-sec"><b>Перегрузки учителей ({report.overloads.length}):</b>
                                                                                <ul>{#each report.overloads as o}<li>{o.name} — {o.got}ч при лимите {o.max}ч</li>{/each}</ul>
                                                                        </div>
                                                                {/if}
                                                        </div>
                                                {/if}
                                        {/if}
                                </section>
                        {/if}
                </main>

                {#if saveModal}
                        <div class="modal-backdrop" role="button" tabindex="-1" on:click={cancelSave} on:keydown={(e) => { if (e.key === 'Escape') cancelSave(); }}>
                                <div class="modal" role="dialog" tabindex="0" on:click|stopPropagation on:keydown|stopPropagation>
                                        <h3>Сохранить файл</h3>
                                        <p class="muted">Файл будет записан в папку ~/Downloads</p>
                                        <input class="modal-input" bind:value={saveModal.filename} placeholder="имя файла" on:keydown={(e) => { if (e.key === "Enter") confirmSave(); }} />
                                        <div class="modal-actions">
                                                <button on:click={cancelSave}>Отмена</button>
                                                <button class="primary" on:click={confirmSave}>Сохранить</button>
                                        </div>
                                </div>
                        </div>
                {/if}
        </div>
</div>

<style>
        :global(body) { margin: 0; }
        .app {
                display: flex;
                min-height: 100vh;
                font-family: ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, Arial, sans-serif;
                color: #1e293b;
                background: #f1f5f9;
        }
        .sidebar {
                width: 232px;
                flex: 0 0 232px;
                background: #ffffff;
                border-right: 1px solid #e2e8f0;
                display: flex;
                flex-direction: column;
                padding: 18px 14px;
                position: sticky;
                top: 0;
                height: 100vh;
                box-sizing: border-box;
        }
        .brand { font-size: 20px; font-weight: 700; color: #4f46e5; padding: 4px 8px 18px; }
        .brand span { color: #0f172a; }
        .nav { display: flex; flex-direction: column; gap: 4px; }
        .nav button {
                display: flex; align-items: center; gap: 10px;
                text-align: left;
                background: transparent; border: none; border-radius: 10px;
                padding: 10px 12px; color: #475569; font-size: 14px; cursor: pointer;
        }
        .nav button:hover { background: #f1f5f9; color: #0f172a; }
        .nav button.active { background: #eef2ff; color: #4f46e5; font-weight: 600; }
        .nav .ico { font-size: 16px; width: 20px; text-align: center; }
        .side-foot { margin-top: auto; display: flex; flex-direction: column; gap: 8px; padding-top: 16px; }
        .side-foot .ghost {
                background: #f8fafc; border: 1px dashed #cbd5e1; color: #475569;
                border-radius: 10px; padding: 8px 10px; cursor: pointer; font-size: 13px;
        }
        .side-foot .ghost:hover { border-color: #4f46e5; color: #4f46e5; }

        .content { flex: 1; display: flex; flex-direction: column; min-width: 0; }
        .topbar {
                display: flex; align-items: center; gap: 14px;
                padding: 14px 24px; background: #ffffff; border-bottom: 1px solid #e2e8f0;
                position: sticky; top: 0; z-index: 5;
        }
        .school { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
        .school select { min-width: 160px; }
        .school-new { width: 150px; }
        main { padding: 24px; }

        input, select {
                background: #fff; color: #1e293b; border: 1px solid #cbd5e1;
                border-radius: 8px; padding: 7px 10px; font-size: 13px; outline: none;
        }
        input:focus, select:focus { border-color: #4f46e5; box-shadow: 0 0 0 3px #e0e7ff; }
        button {
                background: #e2e8f0; color: #1e293b; border: none; border-radius: 8px;
                padding: 8px 14px; cursor: pointer; font-size: 13px; font-weight: 500;
        }
        button:hover { background: #cbd5e1; }
        button.primary { background: #4f46e5; color: #fff; }
        button.primary:hover { background: #4338ca; }
        button.sm { padding: 5px 10px; font-size: 12px; }
        button.danger { background: #fee2e2; color: #b91c1c; }
        button.danger:hover { background: #fecaca; }
        button.mini { padding: 4px 8px; font-size: 12px; background: #f1f5f9; }
        button.mini:hover { background: #e2e8f0; }
        .chk { display: inline-flex; align-items: center; gap: 5px; font-size: 13px; color: #475569; }
        .constraints-form { display: flex; flex-wrap: wrap; gap: 10px; align-items: flex-end; }
        .field { display: flex; flex-direction: column; gap: 2px; }
        .field-label { font-size: 11px; color: #64748b; font-weight: 500; text-transform: uppercase; letter-spacing: 0.5px; }
        .constraints-list li { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
        .con-summary { flex: 1; line-height: 1.4; }
        .chk.warn { color: #b45309; }
        .badge-warn { display: inline-block; background: #fde68a; color: #92400e; padding: 1px 6px; border-radius: 6px; font-size: 10px; font-weight: 600; text-transform: uppercase; letter-spacing: 0.5px; }
        .chk input { width: auto; }
        .row { display: flex; gap: 8px; margin-bottom: 12px; flex-wrap: wrap; align-items: center; }

        .cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 18px; }
        .card {
                background: #fff; border: 1px solid #e2e8f0; border-radius: 14px;
                padding: 18px; box-shadow: 0 1px 2px rgba(15,23,42,0.04);
        }
        .card-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
        .card-head h2 { margin: 0; font-size: 16px; color: #0f172a; }
        h3 { font-size: 14px; color: #334155; margin: 16px 0 8px; }

        .csvbar { display: inline-flex; align-items: center; gap: 4px; font-size: 12px; }
        .file { background: #f1f5f9; padding: 4px 8px; border-radius: 8px; cursor: pointer; }
        .file:hover { background: #e2e8f0; }
        .file input { display: none; }

        .list { list-style: none; padding: 0; margin: 0; max-height: 220px; overflow: auto; }
        .list li { padding: 6px 8px; border-bottom: 1px solid #f1f5f9; font-size: 13px; }
        .list li:last-child { border-bottom: none; }
        .muted { color: #94a3b8; }

        table.data { border-collapse: collapse; width: 100%; margin-top: 8px; }
        table.data th, table.data td { border-bottom: 1px solid #e2e8f0; padding: 8px 10px; text-align: left; font-size: 13px; }
        table.data thead th { background: #f8fafc; color: #64748b; font-weight: 600; font-size: 12px; }
        table.data tbody tr:hover { background: #f8fafc; }
        table.data td.act { width: 1%; white-space: nowrap; }

        .gen-bar { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; margin-bottom: 12px; }
        .gen-bar .sep { flex-basis: 100%; height: 0; }
        .gen-bar .num { width: 56px; }
        .pager { display: inline-flex; align-items: center; gap: 6px; }
        .status { background: #ecfdf5; border: 1px solid #a7f3d0; color: #065f46; padding: 8px 12px; border-radius: 10px; font-size: 13px; }
        .hint { color: #94a3b8; font-size: 12px; margin: 6px 0 14px; }
        .empty { color: #94a3b8; padding: 24px; text-align: center; background: #f8fafc; border-radius: 10px; }
        .grid-scroll { overflow-x: auto; }
        .class-block { margin-bottom: 22px; min-width: 520px; }
        .class-block h3 { margin: 0 0 8px; font-size: 14px; color: #0f172a; }
        .class-block table { border-collapse: collapse; width: 100%; }
        .class-block th, .class-block td { border: 1px solid #e2e8f0; padding: 6px 8px; text-align: center; font-size: 12px; height: 38px; user-select: none; -webkit-user-select: none; touch-action: none; }
        .class-block th { background: #f8fafc; color: #64748b; font-weight: 600; }
        .class-block td.day { background: #f1f5f9; font-weight: 600; white-space: nowrap; }
        td.filled { cursor: grab; border-radius: 4px; font-weight: 500; }
        td.filled:active { cursor: grabbing; }
        td.filled.conflict { background: #b91c1c !important; color: #fff; }
        .cell-x { position: absolute; top: 1px; right: 1px; border: none; background: rgba(0,0,0,0.18); color: #fff; width: 16px; height: 16px; line-height: 14px; border-radius: 4px; cursor: pointer; font-size: 10px; padding: 0; }
        td.filled { position: relative; }
        td.filled.selected { outline: 3px solid #1d4ed8; outline-offset: -2px; }
        .drag-ghost { position: fixed; z-index: 9999; pointer-events: none; transform: translate(-50%, -50%); background: #1d4ed8; color: #fff; padding: 2px 8px; border-radius: 6px; font-size: 12px; max-width: 200px; box-shadow: 0 4px 14px rgba(0,0,0,0.3); }
        .class-block table.compact th, .class-block table.compact td { padding: 1px 3px; font-size: 9px; height: 22px; }
        .class-block.compact h3 { font-size: 11px; margin: 0 0 4px; }

        .toast { background: #16a34a; color: #fff; padding: 8px 14px; border-radius: 10px; font-size: 13px; margin-left: auto; }
        .ver { margin-left: auto; font-size: 12px; color: #94a3b8; font-family: ui-monospace, monospace; }
        .modal-backdrop { position: fixed; inset: 0; background: rgba(15,23,42,0.5); display: flex; align-items: center; justify-content: center; z-index: 10000; }
        .modal { background: #fff; border-radius: 14px; padding: 22px; width: 360px; max-width: 90vw; box-shadow: 0 20px 60px rgba(0,0,0,0.3); }
        .modal h3 { margin: 0 0 6px; font-size: 16px; color: #0f172a; }
        .modal-input { width: 100%; margin: 12px 0; box-sizing: border-box; }
        .modal-actions { display: flex; justify-content: flex-end; gap: 8px; }
        .report { margin-top: 14px; border: 1px solid #fecaca; background: #fef2f2; border-radius: 10px; padding: 10px 14px; font-size: 13px; }
        .report h3 { margin: 0 0 6px; font-size: 13px; color: #b91c1c; }
        .rep-sec { margin: 6px 0; }
        .rep-sec ul { margin: 4px 0 0; padding-left: 18px; }
        .rep-sec li { margin: 2px 0; }
</style>
