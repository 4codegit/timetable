#include "bind.h"

#include <cstdlib>
#include <limits>
#include <map>
#include <vector>

#include "ortools/sat/cp_model.h"
#include "ortools/sat/cp_model_solver.h"
#include "ortools/sat/sat_parameters.pb.h"

using namespace operations_research::sat;
using operations_research::Domain;

namespace {
struct Occ {
  int lesson;
  int cls;
  int teacher;
  int subject;
  int req_type;  // 0 = any
};
}  // namespace

extern "C" ScheduleResult* ortools_solve(
    int num_lessons, const int* lesson_hours, const int* lesson_class,
    const int* lesson_teacher, const int* lesson_subject,
    const int* lesson_req_type, int num_rooms, const int* room_ids,
    const int* room_type, int days_per_week, int slots_per_day,
    int num_constraints, const CConstraint* constraints, int time_limit_ms,
    int workers) {
  CpModelBuilder cp;

  std::vector<Occ> occ;
  for (int i = 0; i < num_lessons; ++i) {
    int h = lesson_hours[i];
    if (h < 1) h = 1;
    int rt = lesson_req_type ? lesson_req_type[i] : 0;
    for (int k = 0; k < h; ++k) {
      occ.push_back({i, lesson_class[i], lesson_teacher[i], lesson_subject[i],
                     rt});
    }
  }
  const int O = static_cast<int>(occ.size());
  const int D = days_per_week, S = slots_per_day, R = num_rooms;
  if (O == 0 || D == 0 || S == 0 || R == 0) return nullptr;

  // entity -> occurrence indices
  std::map<int, std::vector<int>> teacherOccs, classOccs;
  std::vector<int> schoolOccs;
  for (int o = 0; o < O; ++o) {
    teacherOccs[occ[o].teacher].push_back(o);
    classOccs[occ[o].cls].push_back(o);
    schoolOccs.push_back(o);
  }

  // x[occurrence][day][slot][room]
  std::vector<std::vector<std::vector<std::vector<BoolVar>>>> x(
      O, std::vector<std::vector<std::vector<BoolVar>>>(
             D, std::vector<std::vector<BoolVar>>(
                    S, std::vector<BoolVar>(R))));
  for (int o = 0; o < O; ++o)
    for (int d = 0; d < D; ++d)
      for (int s = 0; s < S; ++s)
        for (int r = 0; r < R; ++r) x[o][d][s][r] = cp.NewBoolVar();

  // each occurrence placed exactly once
  for (int o = 0; o < O; ++o) {
    std::vector<BoolVar> vars;
    for (int d = 0; d < D; ++d)
      for (int s = 0; s < S; ++s)
        for (int r = 0; r < R; ++r) vars.push_back(x[o][d][s][r]);
    cp.AddExactlyOne(vars);
  }

  // room-type compatibility (req_type 0 means any)
  for (int o = 0; o < O; ++o) {
    int rt = occ[o].req_type;
    if (rt == 0) continue;
    for (int d = 0; d < D; ++d)
      for (int s = 0; s < S; ++s)
        for (int r = 0; r < R; ++r) {
          int rtype = room_type[r];
          if (rtype != 0 && rtype != rt) {
            cp.AddEquality(x[o][d][s][r], false);
          }
        }
  }

  auto cellVars = [&](const std::vector<int>& occs, int d, int s) {
    std::vector<BoolVar> v;
    for (int o : occs)
      for (int r = 0; r < R; ++r) v.push_back(x[o][d][s][r]);
    return v;
  };
  auto windowVars = [&](const std::vector<int>& occs, int d, int a, int b) {
    std::vector<BoolVar> v;
    for (int s = a; s <= b && s < S; ++s)
      for (int o : occs)
        for (int r = 0; r < R; ++r) v.push_back(x[o][d][s][r]);
    return v;
  };

  // hard uniqueness: teacher / class at most one per (day,slot); room at most one per (day,slot,room)
  for (int d = 0; d < D; ++d) {
    for (int s = 0; s < S; ++s) {
      for (const auto& kv : teacherOccs)
        if (!kv.second.empty()) cp.AddAtMostOne(cellVars(kv.second, d, s));
      for (const auto& kv : classOccs)
        if (!kv.second.empty()) cp.AddAtMostOne(cellVars(kv.second, d, s));
      for (int r = 0; r < R; ++r) {
        std::vector<BoolVar> rv;
        for (int o = 0; o < O; ++o) rv.push_back(x[o][d][s][r]);
        cp.AddAtMostOne(rv);
      }
    }
  }

  // constraint application
  auto occSetOf = [&](int et, int id) -> const std::vector<int>* {
    if (et == 0) return &teacherOccs[id];
    if (et == 1) return &classOccs[id];
    if (et == 3) return &schoolOccs;
    return nullptr;
  };

  std::vector<LinearExpr> penalties;
  const int afternoon = (S > 4) ? 4 : (S - 1);

  for (int ci = 0; ci < num_constraints; ++ci) {
    const CConstraint& c = constraints[ci];
    int start = 0, end = S - 1;
    if (c.slot_start >= 0) start = c.slot_start;
    if (c.slot_end >= 0) end = c.slot_end;
    if (end >= S) end = S - 1;

    auto rangeSlots = [&](int day) -> std::vector<int> {
      std::vector<int> out;
      if (day >= 0) {
        for (int s = start; s <= end; ++s) out.push_back(s);
      } else {
        for (int d = 0; d < D; ++d)
          for (int s = start; s <= end; ++s) out.push_back(d * 1000 + s);
      }
      return out;
    };

    switch (c.ctype) {
      case 0:  // teacher_unavailable
      case 1: {  // class_unavailable
        const std::vector<int>* set = occSetOf(c.entity_type, c.entity_id);
        if (!set) break;
        for (int d = 0; d < D; ++d) {
          if (c.day >= 0 && d != c.day) continue;
          for (int s = start; s <= end; ++s)
            for (int o : *set)
              for (int r = 0; r < R; ++r) cp.AddEquality(x[o][d][s][r], false);
        }
        break;
      }
      case 2: {  // room_unavailable
        int room = c.entity_id;
        if (room < 0 || room >= R) break;
        for (int d = 0; d < D; ++d) {
          if (c.day >= 0 && d != c.day) continue;
          for (int s = start; s <= end; ++s)
            for (int o = 0; o < O; ++o) cp.AddEquality(x[o][d][s][room], false);
        }
        break;
      }
      case 3: {  // max_consecutive
        if (c.value <= 0) break;
        const std::vector<int>* set = occSetOf(c.entity_type, c.entity_id);
        if (!set) break;
        for (int d = 0; d < D; ++d) {
          for (int a = 0; a + c.value < S; ++a) {
            std::vector<BoolVar> w = windowVars(*set, d, a, a + c.value);
            cp.AddLinearConstraint(LinearExpr::Sum(w), Domain(0, c.value));
          }
        }
        break;
      }
      case 4: {  // lunch_break (require a free slot in [start,end])
        const std::vector<int>* set = occSetOf(c.entity_type, c.entity_id);
        if (!set) break;
        int window = end - start + 1;
        if (window <= 0) break;
        for (int d = 0; d < D; ++d) {
          std::vector<BoolVar> w = windowVars(*set, d, start, end);
          cp.AddLinearConstraint(LinearExpr::Sum(w), Domain(0, window - 1));
        }
        break;
      }
      case 5: {  // max_lessons_per_day
        if (c.value <= 0) break;
        const std::vector<int>* set = occSetOf(c.entity_type, c.entity_id);
        if (!set) break;
        for (int d = 0; d < D; ++d) {
          std::vector<BoolVar> w = cellVars(*set, d, 0);
          for (int s = 1; s < S; ++s) {
            std::vector<BoolVar> more = cellVars(*set, d, s);
            w.insert(w.end(), more.begin(), more.end());
          }
          cp.AddLinearConstraint(LinearExpr::Sum(w), Domain(0, c.value));
        }
        break;
      }
      case 6: {  // min_lessons_per_day (soft)
        if (c.value <= 0) break;
        const std::vector<int>* set = occSetOf(c.entity_type, c.entity_id);
        if (!set) break;
        for (int d = 0; d < D; ++d) {
          std::vector<BoolVar> w = cellVars(*set, d, 0);
          for (int s = 1; s < S; ++s) {
            std::vector<BoolVar> more = cellVars(*set, d, s);
            w.insert(w.end(), more.begin(), more.end());
          }
          IntVar p = cp.NewIntVar(Domain(0, c.value)).WithName("minpen");
          LinearExpr lhs = LinearExpr::Sum(w);
          lhs = lhs + LinearExpr(p);
          cp.AddLinearConstraint(lhs, Domain(c.value, std::numeric_limits<int64_t>::max()));
          penalties.push_back(LinearExpr(p));
        }
        break;
      }
      case 7: {  // prefer_morning (soft): penalize afternoon placements
        for (int o = 0; o < O; ++o)
          for (int d = 0; d < D; ++d)
            for (int s = afternoon; s < S; ++s)
              for (int r = 0; r < R; ++r) penalties.push_back(LinearExpr(x[o][d][s][r]));
        break;
      }
      default:
        break;
    }
  }

  if (!penalties.empty()) {
    LinearExpr obj = penalties[0];
    for (size_t i = 1; i < penalties.size(); ++i) obj = obj + penalties[i];
    cp.Minimize(obj);
  }

  SatParameters params;
  params.set_max_time_in_seconds(static_cast<double>(time_limit_ms) / 1000.0);
  if (workers > 0) params.set_num_search_workers(workers);

  CpModelProto model_proto = cp.Build();
  const CpSolverResponse response = SolveWithParameters(model_proto, params);
  if (response.status() != CpSolverStatus::OPTIMAL &&
      response.status() != CpSolverStatus::FEASIBLE) {
    return nullptr;
  }

  ScheduleResult* res =
      static_cast<ScheduleResult*>(malloc(sizeof(ScheduleResult)));
  res->count = O;
  res->lesson_ids = static_cast<int*>(malloc(sizeof(int) * O));
  res->class_ids = static_cast<int*>(malloc(sizeof(int) * O));
  res->teacher_ids = static_cast<int*>(malloc(sizeof(int) * O));
  res->subject_ids = static_cast<int*>(malloc(sizeof(int) * O));
  res->room_ids = static_cast<int*>(malloc(sizeof(int) * O));
  res->days = static_cast<int*>(malloc(sizeof(int) * O));
  res->slots = static_cast<int*>(malloc(sizeof(int) * O));

  for (int o = 0; o < O; ++o) {
    bool found = false;
    for (int d = 0; d < D && !found; ++d) {
      for (int s = 0; s < S && !found; ++s) {
        for (int r = 0; r < R && !found; ++r) {
           if (response.solution(x[o][d][s][r].index()) > 0) {
            res->lesson_ids[o] = occ[o].lesson;
            res->class_ids[o] = occ[o].cls;
            res->teacher_ids[o] = occ[o].teacher;
            res->subject_ids[o] = occ[o].subject;
            res->room_ids[o] = room_ids[r];
            res->days[o] = d;
            res->slots[o] = s;
            found = true;
          }
        }
      }
    }
  }
  return res;
}

extern "C" void free_schedule_result(ScheduleResult* r) {
  if (!r) return;
  free(r->lesson_ids);
  free(r->class_ids);
  free(r->teacher_ids);
  free(r->subject_ids);
  free(r->room_ids);
  free(r->days);
  free(r->slots);
  free(r);
}
