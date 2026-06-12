// @{"req": ["REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-043", "REQ-FEAUTH-148", "REQ-FEAUTH-150", "REQ-FEADMIN-014", "REQ-FEADMIN-015", "REQ-FEONBOARD-001"]}
import { h } from 'vue'
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
import StudentLayout from '@/layouts/StudentLayout.vue'
import GettingStartedView from '@/views/GettingStartedView.vue'
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

// @{"req": ["REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-043", "REQ-FEAUTH-148", "REQ-FEAUTH-150", "REQ-FEADMIN-014", "REQ-FEADMIN-015", "REQ-FEONBOARD-001"]}
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
      // /consent is deliberately NOT nested under StudentLayout. It is a gate
      // page that must be reachable before the student has passed the consent
      // guard, and it has its own full-page design. Wrapping it in the student
      // chrome would embed it inside a layout that assumes a consented session.
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
      // All student-facing routes are nested under StudentLayout so that the
      // persistent top-bar nav (Courses, Getting Started, Logout) is always
      // visible. Deep-link paths are unchanged: every child path is absolute
      // relative to the parent so existing bookmarks continue to work.
      // /consent is excluded (see its own entry above).
      //
      // The redirect also makes this record the single owner of the bare "/"
      // path (a second "/" record would shadow this one and trip Vue Router's
      // unintentional-redirect dev warning): "/" goes to /login, and guard
      // rule 3 immediately forwards already-authenticated users to their role
      // home. Without this, "/" fell through to the catch-all blank page.
      path: '/',
      component: StudentLayout,
      redirect: '/login',
      meta: { requiresAuth: true, requiredRole: 'student' },
      children: [
        {
          path: 'courses',
          name: 'courses',
          component: CourseDashboardView,
          meta: { requiresAuth: true, requiredRole: 'student' }
        },
        {
          path: 'courses/:id/intake',
          name: 'course-intake',
          component: IntakeChatView,
          meta: { requiresAuth: true, requiredRole: 'student' }
        },
        {
          path: 'courses/:id/syllabus',
          name: 'course-syllabus',
          component: SyllabusView,
          meta: { requiresAuth: true, requiredRole: 'student' }
        },
        {
          path: 'courses/:id/generating',
          name: 'course-generating',
          component: GeneratingView,
          meta: { requiresAuth: true, requiredRole: 'student' }
        },
        {
          path: 'courses/:id/hub',
          name: 'course-hub',
          component: CourseHubView,
          meta: { requiresAuth: true, requiredRole: 'student' }
        },
        {
          path: 'courses/:id/sections/:sectionId',
          name: 'section-reader',
          component: SectionReaderView,
          meta: { requiresAuth: true, requiredRole: 'student' }
        },
        {
          path: 'courses/:id/homework/:hwId',
          name: 'homework',
          component: HomeworkSubmissionView,
          meta: { requiresAuth: true, requiredRole: 'student' }
        },
        {
          path: 'courses/:id/grades',
          name: 'grades',
          component: GradesView,
          meta: { requiresAuth: true, requiredRole: 'student' }
        },
        {
          path: 'courses/:id/badges',
          name: 'badges',
          component: BadgesView,
          meta: { requiresAuth: true, requiredRole: 'student' }
        },
        {
          // LEAD AMENDMENT: getting-started is role-scoped. A single shared
          // path cannot render inside two different layouts — Vue Router
          // resolves a path to one route record by declaration order, so a
          // standalone /getting-started entry would always win and render
          // with NO layout chrome, hiding the persistent nav (and its
          // logout button, REQ-FEAUTH-118) on this page. Students get
          // /getting-started inside StudentLayout here; admins get
          // /admin/getting-started inside AdminLayout below. The single
          // GettingStartedView checks auth.isAdmin to pick the step list.
          path: 'getting-started',
          name: 'getting-started',
          component: GettingStartedView,
          meta: { requiresAuth: true, requiredRole: 'student' }
        }
      ]
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
        },
        {
          // Admin counterpart of the student getting-started child route —
          // renders inside AdminLayout's sidebar chrome (see LEAD AMENDMENT
          // comment on the student route for why the path is role-scoped).
          path: 'getting-started',
          name: 'getting-started-admin',
          component: GettingStartedView,
          meta: { requiresAuth: true, requiredRole: 'admin' }
        }
      ]
    },
    {
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      // Must be a render function, not a `template:` string — the production
      // bundle ships the runtime-only Vue build (no template compiler), where
      // a string template silently renders nothing (a blank page).
      component: { render: () => h('div', 'Page not found') },
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

  // 6. Admins have no consent obligation (REQ-SECURITY-005 is student-scoped);
  // keep them out of the consent gate page so they never land on a chrome-less
  // orphan view with no nav or logout.
  if (auth.isAuthenticated && auth.isAdmin && auth.isConsented && to.path === '/consent') {
    return '/admin/users'
  }

  // 7. Authenticated but consent not yet recorded — redirect to /consent
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
