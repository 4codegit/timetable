<script>
	import {
		Greet, CreateSchool, ListSchools,
		CreateTeacher, ListTeachers, CreateSubject, ListSubjects,
		CreateClass, ListClasses, CreateRoom, ListRooms,
		CreateLesson, ListLessons, DeleteLesson,
		CreateConstraint, ListConstraints,
		Generate, GeneratePrecise, MoveEntry, ReplaceSchedule, ListSchedule, ExportAll, ImportAll, ScheduleCSV, ExportRefsCSV, ImportRefsCSV, GetSchoolSettings, UpdateSchoolSettings
	} from "../wailsjs/go/main/App";

	let schools = [];
	let activeSchoolID = 0;
	let newSchoolName = "Моя школа";
	let tab = "refs";
	let msg = "";

	let teachers = [], subjects = [], classes = [], rooms = [], lessons = [], constraints = [], schedule = [];

	// form models
	let t = { name: "", short_name: "", max_hours_per_week: 30 };
	let s = { name: "", short_name: "", requires_room_type: "any" };
	let c = { name: "", grade: 0, student_count: 0, subgroup_of: null };
	let r = { name: "", capacity: 30, room_type: "any" };
	let l = { class_id: 0, subject_id: 0, teacher_id: 0, hours_per_week: 1, min_gap_days: 1, can_split: false, preferred_rooms: "[]" };
	let con = { type: "teacher_unavailable", entity_type: "teacher", entity_id: 0, day_of_week: null, timeslot_start: null, timeslot_end: null, weight: 100, is_hard: true };

	let days = 6, slots = 8;
	let bellPeriods = [];   // [{start,end}] length = slots
	let newPeriodStart = "08:00";
	let newPeriodEnd = "08:45";
	let genResult = null;
	let usePrecise = false;
	let viewMode = "class";
	$: rows = viewMode === "teacher"
		? teachers.map((t) => ({ id: t.id, label: t.name }))
		: viewMode === "room"
		? rooms.map((r) => ({ id: r.id, label: r.name }))
		: classes.map((c) => ({ id: c.id, label: c.name }));

	// export / print
	let exportMode = "school";   // school | class | teacher | room
	let pageSize = "A2";          // A0 | A1 | A2 | A3 | A4
	let orientation = "landscape";// portrait | landscape
	let compact = false;
	let classPage = 0;
	const classesPerPage = 10;
	$: visibleRows = viewMode === "class"
		? rows.slice(classPage * classesPerPage, classPage * classesPerPage + classesPerPage)
		: rows;
	$: totalClassPages = Math.max(1, Math.ceil(classes.length / classesPerPage));

	function flash(m) { msg = m; setTimeout(() => msg = "", 3000); }

	async function loadSchools() {
		schools = await ListSchools();
		if (schools.length && !activeSchoolID) activeSchoolID = schools[0].id;
		await loadSettings();
	}
	async function createSchool() {
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
		[teachers, subjects, classes, rooms, lessons, constraints] = await Promise.all([
			ListTeachers(activeSchoolID), ListSubjects(activeSchoolID),
			ListClasses(activeSchoolID), ListRooms(activeSchoolID),
			ListLessons(activeSchoolID), ListConstraints(activeSchoolID)
		]);
	}
	async function reloadSchedule() {
		if (!activeSchoolID) return;
		schedule = await ListSchedule(activeSchoolID);
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

	async function addTeacher() { await CreateTeacher({ ...t, school_id: activeSchoolID }); t = { name: "", short_name: "", max_hours_per_week: 30 }; await reloadRefs(); }
	async function addSubject() { await CreateSubject({ ...s, school_id: activeSchoolID }); s = { name: "", short_name: "", requires_room_type: "any" }; await reloadRefs(); }
	async function addClass() { await CreateClass({ ...c, school_id: activeSchoolID }); c = { name: "", grade: 0, student_count: 0 }; await reloadRefs(); }
	async function addRoom() { await CreateRoom({ ...r, school_id: activeSchoolID }); r = { name: "", capacity: 30, room_type: "any" }; await reloadRefs(); }
	async function addLesson() { await CreateLesson({ ...l, school_id: activeSchoolID }); l = { class_id: 0, subject_id: 0, teacher_id: 0, hours_per_week: 1, min_gap_days: 1, can_split: false, preferred_rooms: "[]" }; await reloadRefs(); }
	async function addConstraint() {
		const payload = { ...con, school_id: activeSchoolID };
		if (payload.entity_type === "school") payload.entity_id = activeSchoolID;
		if (payload.day_of_week === null || payload.day_of_week === "") delete payload.day_of_week;
		if (payload.timeslot_start === null || payload.timeslot_start === "") delete payload.timeslot_start;
		if (payload.timeslot_end === null || payload.timeslot_end === "") delete payload.timeslot_end;
		await CreateConstraint(payload);
		await reloadRefs();
	}
	async function removeLesson(id) { await DeleteLesson(id); await reloadRefs(); }

	async function generate() {
		await pushHistory();
		const occurrences = lessons.reduce((a, l) => a + (l.hours_per_week || 0), 0);
		if (!usePrecise && occurrences > 200) {
			usePrecise = true;
			flash("Крупная школа: включён точный CP-SAT (OR-Tools). Для 35+ классов нужна сборка с OR-Tools.");
		}
		genResult = usePrecise ? await GeneratePrecise(activeSchoolID, days, slots) : await Generate(activeSchoolID, days, slots);
		await reloadSchedule();
		flash(`Размещено ${genResult.placed}/${genResult.total}, нарушений (мягких): ${genResult.violations}`);
	}
	async function generatePrecise() {
		await pushHistory();
		genResult = await GeneratePrecise(activeSchoolID, days, slots);
		await reloadSchedule();
		flash(`CP-SAT: размещено ${genResult.placed}/${genResult.total}`);
	}
	async function onDrop(e, classID, day, slot) {
		e.preventDefault();
		const id = parseInt(e.dataTransfer.getData("text/plain"));
		if (!id) return;
		const src = schedule.find(en => en.id === id);
		if (!src) return;
		await pushHistory();
		const target = schedule.find(en => en.class_id === classID && en.day_of_week === day && en.timeslot === slot);
		if (target && target.id !== id) {
			await MoveEntry(target.id, src.day_of_week, src.timeslot); // swap
		}
		await MoveEntry(id, day, slot);
		await reloadSchedule();
	}
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
			conflict: conflictIDs.has(e.id),
			label: subjName(subjects, e.subject_id) + " (" + teachName(teachers, e.teacher_id) + ") " + (rooms.find(r => r.id === e.room_id)?.name || "")
		};
	}

	async function exportJSON() {
		const snap = await ExportAll(activeSchoolID);
		download(JSON.stringify(snap, null, 2), "school.json", "application/json");
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
		download(csv, "schedule.csv", "text/csv");
	}
	async function exportPDF() {
		const html = buildExportHTML();
		const iframe = document.createElement("iframe");
		iframe.style.position = "fixed";
		iframe.style.width = "0";
		iframe.style.height = "0";
		iframe.style.border = "0";
		document.body.appendChild(iframe);
		const doc = iframe.contentWindow.document;
		doc.open();
		doc.write(html);
		doc.close();
		iframe.contentWindow.focus();
		iframe.contentWindow.print();
		setTimeout(() => {
			if (iframe.parentNode) iframe.parentNode.removeChild(iframe);
		}, 1500);
	}
	function entityRows(kind) {
		if (kind === "teacher") return teachers.map((t) => ({ id: t.id, label: t.name }));
		if (kind === "room") return rooms.map((r) => ({ id: r.id, label: r.name }));
		return classes.map((c) => ({ id: c.id, label: c.name }));
	}
	function exportBlock(row, kind, pageBreak) {
		let h = pageBreak ? '<div class="page">' : "<div>";
		h += "<h2>" + escapeHtml(row.label) + "</h2><table><thead><tr><th>День</th>";
		for (let si = 0; si < slots; si++) {
			const lbl = periodLabel(si);
			h += "<th>П" + (si + 1) + (lbl ? " " + lbl : "") + "</th>";
		}
		h += "</tr></thead><tbody>";
		for (let di = 0; di < days; di++) {
			h += "<tr><td class=\"day\">" + dayName(di) + "</td>";
			for (let si = 0; si < slots; si++) {
				const cell = cellAt(kind, row.id, di, si);
				h += "<td>" + (cell ? escapeHtml(cell.label) : "") + "</td>";
			}
			h += "</tr>";
		}
		return h + "</tbody></table></div>";
	}
	function buildExportHTML() {
		const kind = exportMode === "school" ? "class" : exportMode;
		const list = entityRows(kind);
		let body = "<h1>Расписание</h1>";
		if (exportMode === "school") {
			for (const row of list) body += exportBlock(row, kind, false);
		} else {
			for (const row of list) body += exportBlock(row, kind, true);
		}
		const cs = exportMode === "school"
			? "table{border-collapse:collapse;width:100%}td,th{border:1px solid #999;padding:1px;font-size:8px}.day{font-size:8px}"
			: "table{border-collapse:collapse;width:100%}td,th{border:1px solid #999;padding:3px;font-size:11px}.day{font-size:11px}";
		return "<html><head><meta charset=\"utf-8\"><title>Расписание</title><" + "style>"
			+ "@page{size:" + pageSize + " " + orientation + ";margin:8mm}"
			+ "body{font-family:Arial,sans-serif;color:#000}.page{break-after:page}" + cs
			+ "<" + "/style></head><body>" + body + "</body></html>";
	}
	function escapeHtml(s) {
		return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
	}
	function download(content, filename, type) {
		const blob = new Blob([content], { type });
		const url = URL.createObjectURL(blob);
		const a = document.createElement("a");
		a.href = url; a.download = filename; a.click();
		URL.revokeObjectURL(url);
	}
	async function downloadRefsCSV(entity) {
		const csv = await ExportRefsCSV(activeSchoolID, entity);
		download(csv, entity + ".csv", "text/csv");
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

	// seed demo data for quick validation
	async function seedDemo() {
		if (!activeSchoolID) { await createSchool(); }
		const sid = activeSchoolID;
		const mk = async (fn) => fn();
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
		for (const c of classes) {
			const n = 8 + (cnt % 3);
			for (let k = 0; k < n; k++) {
				const subj = subs[(c.grade * 3 + k) % subs.length];
				const teacher = teachIDs[ti % teachIDs.length]; ti++;
				const hours = 2 + ((c.grade + k) % 4);
				await CreateLesson({ school_id: sid, class_id: c.id, subject_id: subjIDs[subj], teacher_id: teacher, hours_per_week: hours, min_gap_days: 1, can_split: false, preferred_rooms: "[]" });
			}
		}
		await reloadRefs();
		flash("Демо (35 классов) загружено: " + classes.length + " классов");
	}

	// ---- template helpers ----
	function className(list, id) { const x = list.find(c => c.id === id); return x ? x.name : "?"; }
	function subjName(list, id) { const x = list.find(s => s.id === id); return x ? x.name : "?"; }
	function teachName(list, id) { const x = list.find(t => t.id === id); return x ? (x.short_name || x.name) : "?"; }
	function dayName(d) { return ["Пн","Вт","Ср","Чт","Пт","Сб","Вс"][d] || ("Д" + (d + 1)); }
	function sI(i) { return i + 1; }
	function periodLabel(si) { const p = bellPeriods[si]; return p && p.start ? p.start + "–" + p.end : ""; }
	function cellFor(classID, day, slot) {
		const e = schedule.find(en => en.class_id === classID && en.day_of_week === day && en.timeslot === slot);
		if (!e) return "";
		return subjName(subjects, e.subject_id) + " (" + teachName(teachers, e.teacher_id) + ") " + (rooms.find(r => r.id === e.room_id)?.name || "");
	}

	loadSchools();
</script>

<div class="app">
	<header>
		<h1>📅 Timetable</h1>
		<div class="school-bar">
			<select bind:value={activeSchoolID} on:change={async () => { await reloadRefs(); await loadSettings(); }}>
				{#each schools as sc}<option value={sc.id}>{sc.name}</option>{/each}
			</select>
			<input bind:value={newSchoolName} placeholder="Новая школа" />
			<button on:click={createSchool}>+ Школа</button>
			<button on:click={seedDemo}>⚡ Демо-данные</button>
			<button on:click={seedDemoLarge}>⚡ Демо 35 классов</button>
		</div>
		{#if msg}<div class="toast">{msg}</div>{/if}
	</header>

	<nav>
		<button class:active={tab === "refs"} on:click={() => tab = "refs"}>Справочники</button>
		<button class:active={tab === "lessons"} on:click={() => tab = "lessons"}>Уроки</button>
		<button class:active={tab === "constraints"} on:click={() => tab = "constraints"}>Ограничения</button>
		<button class:active={tab === "settings"} on:click={() => tab = "settings"}>Настройки</button>
		<button class:active={tab === "schedule"} on:click={() => tab = "schedule"}>Расписание</button>
	</nav>

	<main>
		{#if tab === "refs"}
			<div class="grid3">
				<section>
					<h2>Учителя <span class="csvbar"><button on:click={() => downloadRefsCSV('teachers')}>⬇ CSV</button><label class="file">⬆<input type="file" accept=".csv" on:change={(e) => importRefsCSV('teachers', e)} /></label></span></h2>
					<div class="row"><input bind:value={t.name} placeholder="Имя" /><input bind:value={t.short_name} placeholder="Кратко" /><input type="number" bind:value={t.max_hours_per_week} /><button on:click={addTeacher}>+</button></div>
					<ul>{#each teachers as x}<li>{x.name} <small>({x.short_name})</small> — {x.max_hours_per_week}ч/нед</li>{/each}</ul>
				</section>
				<section>
					<h2>Предметы <span class="csvbar"><button on:click={() => downloadRefsCSV('subjects')}>⬇ CSV</button><label class="file">⬆<input type="file" accept=".csv" on:change={(e) => importRefsCSV('subjects', e)} /></label></span></h2>
					<div class="row"><input bind:value={s.name} placeholder="Название" /><input bind:value={s.short_name} placeholder="Кратко" /><input bind:value={s.requires_room_type} placeholder="Тип каб." /><button on:click={addSubject}>+</button></div>
					<ul>{#each subjects as x}<li>{x.name} <small>({x.short_name})</small></li>{/each}</ul>
				</section>
				<section>
					<h2>Классы <span class="csvbar"><button on:click={() => downloadRefsCSV('classes')}>⬇ CSV</button><label class="file">⬆<input type="file" accept=".csv" on:change={(e) => importRefsCSV('classes', e)} /></label></span></h2>
					<div class="row">
						<input bind:value={c.name} placeholder="10А" />
						<input type="number" bind:value={c.grade} placeholder="Класс" />
						<input type="number" bind:value={c.student_count} placeholder="Уч-ся" />
						<select bind:value={c.subgroup_of}><option value={null}>— целый класс —</option>{#each classes as x}<option value={x.id}>{x.name} (подгруппа)</option>{/each}</select>
						<button on:click={addClass}>+</button>
					</div>
					<ul>{#each classes as x}<li>{x.name} — {x.student_count} чел.{x.subgroup_of ? " · подгруппа" : ""}</li>{/each}</ul>
				</section>
				<section>
					<h2>Кабинеты <span class="csvbar"><button on:click={() => downloadRefsCSV('rooms')}>⬇ CSV</button><label class="file">⬆<input type="file" accept=".csv" on:change={(e) => importRefsCSV('rooms', e)} /></label></span></h2>
					<div class="row"><input bind:value={r.name} placeholder="301" /><input type="number" bind:value={r.capacity} /><input bind:value={r.room_type} placeholder="Тип" /><button on:click={addRoom}>+</button></div>
					<ul>{#each rooms as x}<li>{x.name} — {x.capacity} мест</li>{/each}</ul>
				</section>
			</div>
		{:else if tab === "lessons"}
			<section>
				<h2>Учебный план (уроки) <span class="csvbar"><button on:click={() => downloadRefsCSV('lessons')}>⬇ CSV</button><label class="file">⬆<input type="file" accept=".csv" on:change={(e) => importRefsCSV('lessons', e)} /></label></span></h2>
				<div class="lesson-form">
					<select bind:value={l.class_id}><option value={0}>Класс</option>{#each classes as x}<option value={x.id}>{x.name}</option>{/each}</select>
					<select bind:value={l.subject_id}><option value={0}>Предмет</option>{#each subjects as x}<option value={x.id}>{x.name}</option>{/each}</select>
					<select bind:value={l.teacher_id}><option value={0}>Учитель</option>{#each teachers as x}<option value={x.id}>{x.name}</option>{/each}</select>
					<input type="number" bind:value={l.hours_per_week} placeholder="Часов/нед" />
					<input type="number" bind:value={l.min_gap_days} placeholder="Мин. дней между" />
					<label><input type="checkbox" bind:checked={l.can_split} /> делить</label>
					<button on:click={addLesson}>+ Добавить урок</button>
				</div>
				<table>
					<thead><tr><th>Класс</th><th>Предмет</th><th>Учитель</th><th>Ч/нед</th><th></th></tr></thead>
					<tbody>
						{#each lessons as x}
							<tr>
								<td>{className(classes, x.class_id)}</td>
								<td>{subjName(subjects, x.subject_id)}</td>
								<td>{teachName(teachers, x.teacher_id)}</td>
								<td>{x.hours_per_week}</td>
								<td><button on:click={() => removeLesson(x.id)}>✕</button></td>
							</tr>
						{/each}
					</tbody>
				</table>
			</section>
		{:else if tab === "constraints"}
			<section>
				<h2>Ограничения</h2>
				<div class="lesson-form">
					<select bind:value={con.type}>
						<option value="teacher_unavailable">Учитель недоступен</option>
						<option value="class_unavailable">Класс недоступен</option>
						<option value="room_unavailable">Кабинет недоступен</option>
						<option value="max_consecutive">Макс. подряд (уроков)</option>
						<option value="lunch_break">Обеденный перерыв</option>
						<option value="max_lessons_per_day">Макс. уроков в день</option>
						<option value="min_lessons_per_day">Мин. уроков в день</option>
						<option value="prefer_morning">Желательно утро</option>
						<option value="max_gaps">Макс. окон</option>
					</select>
					<select bind:value={con.entity_type}><option value="teacher">Учитель</option><option value="class">Класс</option><option value="room">Кабинет</option><option value="school">Школа</option></select>
					{#if con.entity_type === "school"}
						<span>вся школа</span>
					{:else}
						<select bind:value={con.entity_id}><option value={0}>Сущность</option>{#if con.entity_type==="teacher"}{#each teachers as x}<option value={x.id}>{x.name}</option>{/each}{:else if con.entity_type==="class"}{#each classes as x}<option value={x.id}>{x.name}</option>{/each}{:else if con.entity_type==="room"}{#each rooms as x}<option value={x.id}>{x.name}</option>{/each}{/if}</select>
					{/if}
					<input type="number" bind:value={con.day_of_week} placeholder="День(0-5)" />
					<input type="number" bind:value={con.timeslot_start} placeholder="Слот с" />
					<input type="number" bind:value={con.timeslot_end} placeholder="Слот по" />
					{#if ["max_consecutive","lunch_break","max_lessons_per_day","min_lessons_per_day"].includes(con.type)}
						<input type="number" bind:value={con.weight} placeholder="Значение" />
					{/if}
					<label><input type="checkbox" bind:checked={con.is_hard} /> жёсткое</label>
					<button on:click={addConstraint}>+ Добавить</button>
				</div>
				<ul>{#each constraints as x}<li>{x.type} → {x.entity_type}#{x.entity_id} {x.is_hard ? "жёсткое" : "мягкое"}</li>{/each}</ul>
			</section>
		{:else if tab === "settings"}
			<section>
				<h2>Настройки школы</h2>
				<div class="row">
					<label>Учебных дней: <input type="number" min="1" max="7" bind:value={days} /></label>
					<label>Уроков в день: <input type="number" min="1" max="14" bind:value={slots} on:change={onSlotsChange} /></label>
					<button on:click={saveSettings}>Сохранить</button>
					<span class="csvbar"><button on:click={() => downloadRefsCSV('periods')}>⬇ CSV звонков</button><label class="file">⬆<input type="file" accept=".csv" on:change={(e) => importRefsCSV('periods', e)} /></label></span>
				</div>
				<h3>Расписание звонков</h3>
				<table>
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
			<section>
				<div class="gen-bar">
					<label>Дней: <input type="number" bind:value={days} style="width:50px" /></label>
					<label>Слотов: <input type="number" bind:value={slots} style="width:50px" /></label>
					<button class="primary" on:click={generate}>⚙ Сгенерировать</button>
					<button on:click={undo}>↶ Отменить</button>
					<label><input type="checkbox" bind:checked={usePrecise} /> точный CP-SAT (OR-Tools)</label>
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
							<button on:click={() => classPage = Math.max(0, classPage - 1)} disabled={classPage === 0}>‹</button>
							<span>{classPage + 1}/{totalClassPages}</span>
							<button on:click={() => classPage = Math.min(totalClassPages - 1, classPage + 1)} disabled={classPage >= totalClassPages - 1}>›</button>
						</span>
					{/if}
					<span class="sep" />
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
							<option value="A0">A0</option>
							<option value="A1">A1</option>
							<option value="A2">A2</option>
							<option value="A3">A3</option>
							<option value="A4">A4</option>
						</select>
					</label>
					<label>Ориент.:
						<select bind:value={orientation}>
							<option value="landscape">альбомн.</option>
							<option value="portrait">книжн.</option>
						</select>
					</label>
					<button on:click={exportPDF}>⬇ PDF</button>
					<button on:click={exportCSV}>⬇ CSV</button>
					<button on:click={exportJSON}>⬇ JSON</button>
					<label class="file">⬆ JSON<input type="file" accept="application/json" on:change={importJSON} /></label>
				</div>
				{#if genResult}<p class="status">Размещено <b>{genResult.placed}/{genResult.total}</b> · мягких нарушений: <b>{genResult.violations}</b></p>{/if}
				<p class="hint">Перетащите урок в другую ячейку, чтобы отредактировать вручную.</p>
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
												draggable={viewMode === "class" && !!cell}
												on:dragstart={(e) => cell && e.dataTransfer.setData("text/plain", String(cell.id))}
												on:dragover={(e) => e.preventDefault()}
												on:drop={viewMode === "class" ? (e) => onDrop(e, row.id, di, si) : null}>{cell ? cell.label : ""}</td>{/each}</tr>
										{/each}
									</tbody>
								</table>
							</div>
						{/each}
					</div>
				{/if}
			</section>
		{/if}
	</main>
</div>

<style>
	.app { font-family: system-ui, sans-serif; color: #e2e8f0; background: #0f172a; min-height: 100vh; }
	header { display: flex; align-items: center; gap: 16px; padding: 12px 20px; background: #1e293b; flex-wrap: wrap; }
	h1 { font-size: 20px; margin: 0; }
	.school-bar { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
	input, select { background: #0f172a; color: #e2e8f0; border: 1px solid #475569; border-radius: 6px; padding: 6px 8px; }
	button { background: #334155; color: #e2e8f0; border: none; border-radius: 6px; padding: 6px 12px; cursor: pointer; }
	button:hover { background: #475569; }
	button.primary { background: #2563eb; }
	button.primary:hover { background: #1d4ed8; }
	nav { display: flex; gap: 4px; padding: 0 20px; background: #111827; }
	nav button { border-radius: 0; background: transparent; border-bottom: 2px solid transparent; }
	nav button.active { border-bottom-color: #2563eb; color: #fff; }
	.toast { background: #16a34a; color: #fff; padding: 6px 12px; border-radius: 6px; }
	main { padding: 20px; }
	.grid3 { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: 16px; }
	.row { display: flex; gap: 6px; margin-bottom: 8px; flex-wrap: wrap; }
	ul { list-style: none; padding: 0; }
	li { padding: 4px 0; border-bottom: 1px solid #1e293b; }
	.lesson-form { display: flex; gap: 6px; flex-wrap: wrap; margin-bottom: 12px; align-items: center; }
	table { border-collapse: collapse; width: 100%; margin-top: 8px; }
	th, td { border: 1px solid #334155; padding: 6px; text-align: left; font-size: 13px; }
	.gen-bar { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; margin-bottom: 12px; }
	.gen-bar .sep { flex-basis: 100%; height: 0; }
	.gen-bar .chk { display: inline-flex; align-items: center; gap: 4px; }
	.pager { display: inline-flex; align-items: center; gap: 6px; }
	.pager button { padding: 2px 8px; }
	table.compact th, table.compact td { padding: 1px 3px; font-size: 9px; }
	.class-block.compact h3 { font-size: 11px; margin: 0 0 2px; }
	.class-block.compact { margin-bottom: 8px; }
	.status { background: #1e293b; padding: 8px 12px; border-radius: 6px; }
	.empty { color: #94a3b8; }
	.hint { color: #94a3b8; font-size: 12px; margin: 4px 0 12px; }
	td.filled { background: #1e3a8a; cursor: grab; }
	td.filled:active { cursor: grabbing; }
	td.filled.conflict { background: #b91c1c; }
	.grid-scroll { overflow-x: auto; }
	.class-block { margin-bottom: 20px; }
	.class-block h3 { margin: 0 0 6px; }
	.day { font-weight: 600; background: #1e293b; }
	.file { background: #334155; padding: 6px 12px; border-radius: 6px; cursor: pointer; }
	.file input { display: none; }
	.csvbar { font-size: 11px; font-weight: normal; }
	.csvbar button { padding: 2px 6px; margin-left: 4px; }
</style>