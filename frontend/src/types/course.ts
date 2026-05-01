export type CourseStatus =
  | 'intake'
  | 'syllabus_draft'
  | 'generating'
  | 'active'
  | 'archived'

export interface Course {
  id: string
  title: string
  status: CourseStatus
  student_id: string
  created_at: string
  updated_at: string
}

export interface CourseListResponse {
  courses: Course[]
}

export interface CourseResponse {
  course: Course
}
