// @{"req": ["REQ-FEADMIN-707"]}
// Types that exactly mirror the GET /admin/courses/{id}/overview response shape
// from internal/admin/oversight_handler.go buildOverviewResponse.

export interface OverviewCourse {
  id: string
  student_id: string
  username: string
  topic: string
  status: string
  created_at: string
  updated_at: string
}

export interface OverviewSyllabus {
  id: string
  content_adoc: string
  version: number
  created_at: string
  approved_at: string | null
}

export interface OverviewSection {
  id: string
  section_index: number
  title: string
  content_adoc: string
  version: number
  citation_verified: boolean
  created_at: string
}

export interface OverviewHomework {
  id: string
  section_index: number
  title: string
  rubric: string
  grade_weight: number
  due_date: string | null
  created_at: string
}

export interface OverviewSubmission {
  id: string
  homework_id: string
  student_id: string
  format: string
  file_path: string
  file_size_bytes: number
  submitted_at: string
  grading_status: string
  raw_score: number | null
}

export interface OverviewGrade {
  id: string
  submission_id: string
  homework_id: string
  raw_score: number
  late_days: number
  late_penalty_rate: number
  late_penalty_amount: number
  badge_waiver_applied: boolean
  badge_improvement: boolean
  final_score: number
  graded_at: string
}

export interface CourseOverviewResponse {
  course: OverviewCourse
  syllabus: OverviewSyllabus | null
  sections: OverviewSection[]
  homework: OverviewHomework[]
  submissions: OverviewSubmission[]
  grades: OverviewGrade[]
}
