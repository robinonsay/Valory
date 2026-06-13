export type CourseStatus =
  | 'intake'
  | 'syllabus_draft'
  | 'syllabus_approved'
  | 'generating'
  | 'active'
  | 'archived'
  | 'completed'

export interface Course {
  id: string
  title: string
  topic: string
  status: CourseStatus
  student_id: string
  created_at: string
  updated_at: string
  assignment_id?: string | null
}

export interface CourseListResponse {
  courses: Course[]
}

// The backend course endpoints (POST /api/v1/courses, GET /api/v1/courses/:id)
// return the course object FLAT — there is no {"course": ...} wrapper. See
// internal/course/handler.go courseToResponse. The wrapped shape here caused
// response.course to be undefined at runtime (navigating to /courses/undefined/intake).
export type CourseResponse = Course
