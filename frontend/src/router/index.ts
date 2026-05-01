// @{"req": ["REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-043", "REQ-FEAUTH-148", "REQ-FEAUTH-150", "REQ-FEADMIN-014", "REQ-FEADMIN-015"]}
import { createRouter, createWebHistory, type RouteLocationNormalized, type RouteLocationRaw } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import LoginView from '@/views/LoginView.vue'
import ConsentView from '@/views/ConsentView.vue'
import PasswordResetView from '@/views/PasswordResetView.vue'
import CourseDashboardView from '@/views/CourseDashboardView.vue'
import IntakeChatView from '@/views/IntakeChatView.vue'
import SyllabusView from '@/views/SyllabusView.vue'
import GeneratingView from '@/views/GeneratingView.vue'
import CourseHubView from '@/views/CourseHubView.vue'
import SectionReaderView from '@/views/SectionReaderView.vue'
import HomeworkSubmissionView from '@/views/HomeworkSubmissionView.vue'
import GradesView from '@/views/GradesView.vue'
import BadgesView from '@/views/BadgesView.vue'
import AdminLayout from '@/layouts/AdminLayout.vue'
import UserManagementView from '@/views/admin/UserManagementView.vue'
import AuditLogView from '@/views/admin/AuditLogView.vue'
import SystemConfigView from '@/views/admin/SystemConfigView.vue'
import CourseOversightView from '@/views/admin/CourseOversightView.vue'

declare module 'vue-router' {
  interface RouteMeta {
    requiresAuth?: boolean
    requiredRole?: 'student' | 'admin'
  }
}

// @{"req": ["REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-043", "REQ-FEAUTH-148", "REQ-FEAUTH-150", "REQ-FEADMIN-014", "REQ-FEADMIN-015"]}
const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: LoginView,
      meta: {}
    },
    {
      path: '/consent',
      name: 'consent',
      component: ConsentView,
      meta: { requiresAuth: true }
    },
    {
      path: '/reset-password',
      name: 'reset-password',
      component: PasswordResetView,
      meta: {}
    },
    {
      path: '/courses',
      name: 'courses',
      component: CourseDashboardView,
      meta: { requiresAuth: true, requiredRole: 'student' }
    },
    {
      path: '/courses/:id/intake',
      name: 'course-intake',
      component: IntakeChatView,
      meta: { requiresAuth: true, requiredRole: 'student' }
    },
    {
      path: '/courses/:id/syllabus',
      name: 'course-syllabus',
      component: SyllabusView,
      meta: { requiresAuth: true, requiredRole: 'student' }
    },
    {
      path: '/courses/:id/generating',
      name: 'course-generating',
      component: GeneratingView,
      meta: { requiresAuth: true, requiredRole: 'student' }
    },
    {
      path: '/courses/:id/hub',
      name: 'course-hub',
      component: CourseHubView,
      meta: { requiresAuth: true, requiredRole: 'student' }
    },
    {
      path: '/courses/:id/sections/:sectionId',
      name: 'section-reader',
      component: SectionReaderView,
      meta: { requiresAuth: true, requiredRole: 'student' }
    },
    {
      path: '/courses/:id/homework/:hwId',
      name: 'homework',
      component: HomeworkSubmissionView,
      meta: { requiresAuth: true, requiredRole: 'student' }
    },
    {
      path: '/courses/:id/grades',
      name: 'grades',
      component: GradesView,
      meta: { requiresAuth: true, requiredRole: 'student' }
    },
    {
      path: '/courses/:id/badges',
      name: 'badges',
      component: BadgesView,
      meta: { requiresAuth: true, requiredRole: 'student' }
    },
    {
      path: '/admin',
      name: 'admin',
      component: AdminLayout,
      redirect: '/admin/users',
      meta: { requiresAuth: true, requiredRole: 'admin' },
      children: [
        {
          path: 'users',
          name: 'admin-users',
          component: UserManagementView,
          meta: { requiresAuth: true, requiredRole: 'admin' }
        },
        {
          path: 'audit',
          name: 'admin-audit',
          component: AuditLogView,
          meta: { requiresAuth: true, requiredRole: 'admin' }
        },
        {
          path: 'config',
          name: 'admin-config',
          component: SystemConfigView,
          meta: { requiresAuth: true, requiredRole: 'admin' }
        },
        {
          path: 'courses',
          name: 'admin-courses',
          component: CourseOversightView,
          meta: { requiresAuth: true, requiredRole: 'admin' }
        }
      ]
    },
    {
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      component: { template: '<div>Page not found</div>' },
      meta: {}
    }
  ]
})

// Export for testing — the guard receives the store's isAuthenticated, isAdmin,
// isStudent, isConsented, isExpired getters and logout action as parameters
// so it is testable without a running router instance.
// @{"req": ["REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-043", "REQ-FEAUTH-148", "REQ-FEAUTH-150", "REQ-FEADMIN-014", "REQ-FEADMIN-015"]}
export function guardFn(
  to: RouteLocationNormalized,
  auth: {
    isAuthenticated: boolean
    isAdmin: boolean
    isStudent: boolean
    isConsented: boolean
    isExpired: boolean
    logout: () => void
  }
): RouteLocationRaw | undefined {
  // Returns the redirect target, or undefined to allow navigation

  // 1. Unauthenticated user trying to reach a protected route
  if (to.meta.requiresAuth && !auth.isAuthenticated) {
    return '/login'
  }

  // 2. Authenticated but expired session — clear state and redirect to login
  if (to.meta.requiresAuth && auth.isExpired) {
    auth.logout()
    return '/login'
  }

  // 3. Already authenticated user navigating to /login — send to their home
  if (auth.isAuthenticated && to.path === '/login') {
    if (auth.isAdmin) {
      return '/admin/users'
    }
    return '/courses'
  }

  // 4. Student trying to reach an admin route
  if (to.meta.requiredRole === 'admin' && !auth.isAdmin) {
    return '/courses'
  }

  // 5. Admin trying to reach a student route
  if (to.meta.requiredRole === 'student' && !auth.isStudent) {
    return '/admin/users'
  }

  // 6. Authenticated but consent not yet recorded — redirect to /consent
  if (auth.isAuthenticated && !auth.isConsented && to.path !== '/consent') {
    return '/consent'
  }

  return undefined
}

// @{"req": ["REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-043", "REQ-FEAUTH-148", "REQ-FEAUTH-150", "REQ-FEADMIN-014", "REQ-FEADMIN-015"]}
router.beforeEach((to, _from, next) => {
  // useAuthStore is called inside the guard so Pinia is already initialised
  const auth = useAuthStore()
  const redirect = guardFn(to, auth)
  if (redirect) {
    next(redirect)
  } else {
    next()
  }
})

export default router
