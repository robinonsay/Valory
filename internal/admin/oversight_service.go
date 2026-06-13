package admin

import (
	"context"

	"github.com/google/uuid"
)

// CourseOverview aggregates all material for a single student course into one
// structure for the GET /admin/courses/{id}/overview response.
type CourseOverview struct {
	Course      CourseOverviewCourse
	Syllabus    *CourseOverviewSyllabus
	Sections    []CourseOverviewSection
	Homework    []CourseOverviewHomework
	Submissions []CourseOverviewSubmission
	Grades      []CourseOverviewGrade
}

// OversightService coordinates the read-only queries that build a full admin
// overview of a student course. It delegates all DB access to OversightRepository
// so that the connection carrying app.current_role='admin' is used consistently.
//
// @{"req": ["REQ-ADMIN-011"]}
type OversightService struct {
	repo *OversightRepository
}

// @{"req": ["REQ-ADMIN-011"]}
func NewOversightService(repo *OversightRepository) *OversightService {
	return &OversightService{repo: repo}
}

// GetCourseOverview assembles the full admin oversight view of a course.
// Returns ErrCourseNotFound when the course does not exist.
//
// @{"req": ["REQ-ADMIN-011"]}
func (s *OversightService) GetCourseOverview(ctx context.Context, courseID uuid.UUID) (CourseOverview, error) {
	course, err := s.repo.GetCourse(ctx, courseID)
	if err != nil {
		return CourseOverview{}, err
	}

	syllabus, hasSyllabus, err := s.repo.GetSyllabus(ctx, courseID)
	if err != nil {
		return CourseOverview{}, err
	}

	sections, err := s.repo.ListSections(ctx, courseID)
	if err != nil {
		return CourseOverview{}, err
	}

	homework, err := s.repo.ListHomework(ctx, courseID)
	if err != nil {
		return CourseOverview{}, err
	}

	submissions, err := s.repo.ListSubmissions(ctx, courseID)
	if err != nil {
		return CourseOverview{}, err
	}

	grades, err := s.repo.ListGrades(ctx, courseID)
	if err != nil {
		return CourseOverview{}, err
	}

	overview := CourseOverview{
		Course:      course,
		Sections:    sections,
		Homework:    homework,
		Submissions: submissions,
		Grades:      grades,
	}
	if hasSyllabus {
		overview.Syllabus = &syllabus
	}
	return overview, nil
}
