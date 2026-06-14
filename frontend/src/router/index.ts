// @{"req": ["REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-043", "REQ-FEAUTH-148", "REQ-FEAUTH-150", "REQ-FEADMIN-014", "REQ-FEADMIN-015", "REQ-FEONBOARD-001", "REQ-FESETUP-001", "REQ-FESETUP-002", "REQ-FESETUP-003", "REQ-SYS-073", "REQ-FEADMIN-710"]}
import { h } from 'vue'
import { createRouter, createWebHistory, type RouteLocationNormalized, type RouteLocationRaw } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useSetupStore } from '@/stores/setup'
import LoginView from '@/views/LoginView.vue'
import OtpVerifyView from '@/views/OtpVerifyView.vue'
import ConsentView from '@/views/ConsentView.vue'
import PasswordResetView from '@/views/PasswordResetView.vue'
import ChangePasswordView from '@/views/ChangePasswordView.vue'
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
import OnboardingChatView from '@/views/OnboardingChatView.vue'
import ProfileView from '@/views/ProfileView.vue'
import UserManagementView from '@/views/admin/UserManagementView.vue'
import AuditLogView from '@/views/admin/AuditLogView.vue'
import SystemConfigView from '@/views/admin/SystemConfigView.vue'
import CourseOversightView from '@/views/admin/CourseOversightView.vue'
import CourseDetailView from '@/views/admin/CourseDetailView.vue'
import AdminAssignmentsView from '@/views/admin/AdminAssignmentsView.vue'
import AdminAssignmentDetailView from '@/views/admin/AdminAssignmentDetailView.vue'
import CourseBuildView from '@/views/CourseBuildView.vue'
import DraftListView from '@/views/admin/DraftListView.vue'
import DraftEditorView from '@/views/admin/DraftEditorView.vue'

declare module 'vue-router' {
  interface RouteMeta {
    requiresAuth?: boolean
    requiredRole?: 'student' | 'admin'
  }
}

// @{"req": ["REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-043", "REQ-FEAUTH-148", "REQ-FEAUTH-150", "REQ-FEADMIN-014", "REQ-FEADMIN-015", "REQ-FEONBOARD-001", "REQ-FEAUTH-202", "REQ-FESETUP-001", "REQ-FESETUP-002", "REQ-FESETUP-003"]}
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
      // @{"req": ["REQ-FEAUTH-202"]}
      path: '/login/verify',
      name: 'login-verify',
      component: OtpVerifyView,
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
      path: '/change-password',
      name: 'change-password',
      component: ChangePasswordView,
      meta: { requiresAuth: true }
    },
    {
      // @{"req": ["REQ-FEPROFILE-001", "REQ-FEPROFILE-004"]}
      // /onboarding is deliberately NOT nested under StudentLayout. It is a gate
      // page that must be reachable before the student navigates to other routes,
      // and it has its own full-page design. Wrapping it in the student chrome
      // would embed it inside a layout that assumes a student has already
      // experienced the onboarding flow.
      path: '/onboarding',
      name: 'onboarding',
      component: OnboardingChatView,
      meta: { requiresAuth: true, requiredRole: 'student' }
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
          // @{"req": ["REQ-SYS-073"]}
          // Interactive layer-by-layer build view for tree_mode=true courses.
          // Reached via CourseDashboardView.navigateToCourse when tree_mode is set.
          path: 'courses/:id/build',
          name: 'course-build',
          component: CourseBuildView,
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
          // @{"req": ["REQ-FEPROFILE-002"]}
          path: 'profile',
          name: 'student-profile',
          component: ProfileView,
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
          // @{"req": ["REQ-FEADMIN-707"]}
          path: 'courses/:id',
          name: 'admin-course-detail',
          component: CourseDetailView,
          meta: { requiresAuth: true, requiredRole: 'admin' }
        },
        {
          // @{"req": ["REQ-FEADMIN-702", "REQ-FEADMIN-703"]}
          path: 'assignments',
          name: 'admin-assignments',
          component: AdminAssignmentsView,
          meta: { requiresAuth: true, requiredRole: 'admin' }
        },
        {
          // @{"req": ["REQ-FEADMIN-704", "REQ-FEADMIN-705", "REQ-FEADMIN-706"]}
          path: 'assignments/:id',
          name: 'admin-assignment-detail',
          component: AdminAssignmentDetailView,
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
        },
        {
          // @{"req": ["REQ-FEADMIN-710"]}
          // Draft list — admin authors a new tree course draft. Listed
          // separately from assignments; published drafts produce assignments
          // the admin routes to from AdminAssignmentDetailView.
          path: 'drafts',
          name: 'admin-drafts',
          component: DraftListView,
          meta: { requiresAuth: true, requiredRole: 'admin' }
        },
        {
          // @{"req": ["REQ-FEADMIN-710"]}
          // Draft editor — interactive tree build view for a single draft.
          // Admin approves/rejects/regenerates nodes and publishes when done.
          path: 'drafts/:id',
          name: 'admin-draft-editor',
          component: DraftEditorView,
          meta: { requiresAuth: true, requiredRole: 'admin' }
        }
      ]
    },
    {
      // @{"req": ["REQ-FESETUP-001", "REQ-FESETUP-002", "REQ-FESETUP-003", "REQ-SYS-071"]}
      // No requiresAuth — guard rule 0 controls access in both directions.
      path: '/setup',
      name: 'setup',
      component: () => import('@/views/SetupView.vue'),
      meta: {}
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

// Export for testing — the guard receives the store's auth state as parameters
// so it is testable without a running router instance. The restorePromise
// parameter (when provided) is awaited before any routing decision is made,
// which blocks navigation until session restore completes (REQ-FEAUTH-170).
// The optional setupState parameter (when provided) enables rule 0: redirect
// all navigation to /setup when needs_setup is true, and redirect /setup to
// /login when needs_setup is false. When setupState is omitted, rule 0 does
// not fire and existing call sites continue to behave as before.
// @{"req": ["REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-043", "REQ-FEAUTH-148", "REQ-FEAUTH-150", "REQ-FEADMIN-014", "REQ-FEADMIN-015", "REQ-FEAUTH-170", "REQ-FEUSER-002", "REQ-FEAUTH-202", "REQ-FEPROFILE-004", "REQ-FESETUP-001", "REQ-FESETUP-002", "REQ-FESETUP-003"]}
export async function guardFn(
  to: RouteLocationNormalized,
  auth: {
    restorePromise?: Promise<void> | null
    isAuthenticated: boolean
    isAdmin: boolean
    isStudent: boolean
    isConsented: boolean
    isExpired: boolean
    mustChangePassword: boolean
    onboardingPrompted: boolean
    pendingTwoFactor: { token: string; expiresAt: number } | null
    logout: () => void
  },
  setupState?: {
    setupPromise?: Promise<void> | null
    needsSetup: boolean | null
  }
): Promise<RouteLocationRaw | undefined> {
  // REQ-FEAUTH-170: await the session-restore promise so navigation BLOCKS
  // until restoreSession() completes. This guarantees that every guard decision
  // sees correct auth state — components never mount in a pre-restore limbo
  // where isAuthenticated is false but a valid session exists.
  if (auth.restorePromise) {
    await auth.restorePromise
  }

  // REQ-FESETUP-001: await setup status before any routing decision — mirrors
  // the auth restorePromise pattern.
  if (setupState?.setupPromise) {
    await setupState.setupPromise
  }

  // @{"req": ["REQ-FESETUP-002"]}
  // Rule 0a: setup required — hard-redirect everything to /setup.
  if (setupState?.needsSetup === true && to.path !== '/setup') {
    return '/setup'
  }

  // @{"req": ["REQ-FESETUP-003"]}
  // Rule 0b: setup complete — /setup is not reachable.
  if (setupState !== undefined && setupState.needsSetup === false && to.path === '/setup') {
    return '/login'
  }

  // Rule 0c: needsSetup is null (check failed or setupState omitted) — allow
  // navigation to proceed. This edge case is rare and self-corrects on next navigation.

  // @{"req": ["REQ-FEAUTH-202"]}
  // 1a. User with pending 2FA: redirect to /login/verify unless already there or navigating to /login
  if (auth.pendingTwoFactor !== null && to.path !== '/login/verify' && to.path !== '/login') {
    return '/login/verify'
  }

  // 1. Unauthenticated user trying to reach a protected route
  if (to.meta.requiresAuth && !auth.isAuthenticated) {
    return '/login'
  }

  // 2. Authenticated but expired session — clear state and redirect to login
  if (to.meta.requiresAuth && auth.isExpired) {
    auth.logout()
    return '/login'
  }

  // 3. REQ-FEUSER-002: flagged users cannot navigate anywhere except /change-password,
  // /logout (implicitly allowed via no requiresAuth), and GET /users/me (no route).
  // must_change_password is a HARD gate that supersedes the consent (rule 8) and
  // onboarding (rule 3a) gates. The early return below is load-bearing: an
  // admin-created student logs in with mustChangePassword=true AND isConsented=false,
  // so without it rule 8 bounces /change-password -> /consent while this rule bounces
  // /consent -> /change-password — an infinite redirect loop that starves the event
  // loop and freezes the SPA with the login button stuck on "Signing in...". Returning
  // here keeps the flagged user on /change-password until they change their password,
  // which clears the flag and re-enables the consent/onboarding gates on the next nav.
  if (auth.mustChangePassword) {
    if (to.path !== '/change-password') {
      return '/change-password'
    }
    return undefined
  }

  // 4. Already authenticated user navigating to /login or /login/verify — send to their home
  if (auth.isAuthenticated && (to.path === '/login' || to.path === '/login/verify')) {
    if (auth.isAdmin) {
      return '/admin/users'
    }
    return '/courses'
  }

  // 5. Student trying to reach an admin route
  if (to.meta.requiredRole === 'admin' && !auth.isAdmin) {
    return '/courses'
  }

  // 6. Admin trying to reach a student route
  if (to.meta.requiredRole === 'student' && !auth.isStudent) {
    return '/admin/users'
  }

  // 7. Admins have no consent obligation (REQ-SECURITY-005 is student-scoped);
  // keep them out of the consent gate page so they never land on a chrome-less
  // orphan view with no nav or logout.
  if (auth.isAuthenticated && auth.isAdmin && auth.isConsented && to.path === '/consent') {
    return '/admin/users'
  }

  // 8. Authenticated but consent not yet recorded — redirect to /consent
  // This check comes before the onboarding check so consent (which is mandatory for all
  // students) takes precedence over onboarding (which is optional/soft).
  if (auth.isAuthenticated && !auth.isConsented && to.path !== '/consent') {
    return '/consent'
  }

  // 3a. @{"req": ["REQ-FEPROFILE-004"]} Student has not been prompted for onboarding
  // and is navigating to anywhere except /onboarding, /change-password, /consent,
  // /login, and /logout-equivalent paths: redirect to '/onboarding'.
  // This rule fires only for students, only when onboarding_prompted = false, and
  // only after the mustChangePassword and consent checks pass. Onboarding is a soft
  // gate that does not block navigation like consent does.
  if (
    auth.isStudent &&
    !auth.onboardingPrompted &&
    to.path !== '/onboarding' &&
    to.path !== '/change-password' &&
    to.path !== '/consent' &&
    to.path !== '/login' &&
    to.path !== '/login/verify'
  ) {
    return '/onboarding'
  }

  return undefined
}

// @{"req": ["REQ-FEAUTH-040", "REQ-FEAUTH-041", "REQ-FEAUTH-042", "REQ-FEAUTH-043", "REQ-FEAUTH-148", "REQ-FEAUTH-150", "REQ-FEADMIN-014", "REQ-FEADMIN-015", "REQ-FEAUTH-170", "REQ-FEUSER-002", "REQ-FEAUTH-202", "REQ-FEPROFILE-004", "REQ-FESETUP-001", "REQ-FESETUP-002", "REQ-FESETUP-003"]}
router.beforeEach(async (to, _from) => {
  // useAuthStore is called inside the guard so Pinia is already initialised.
  // Awaiting getRestorePromise() blocks this navigation until restoreSession()
  // completes, so protected components never mount before auth state is known.
  const auth = useAuthStore()
  const setup = useSetupStore()
  // REQ-FESETUP-001/002: resolve setup status BEFORE the first routing decision.
  // checkSetupStatus() is idempotent (returns the cached/in-flight promise), so
  // this fetches at most once; awaiting it here closes the first-navigation race
  // where needsSetup was still null — without it the guard fell through to /login
  // on a fresh install instead of redirecting to the first-run setup wizard.
  await setup.checkSetupStatus()
  const redirect = await guardFn(
    to,
    {
      restorePromise: auth.getRestorePromise(),
      isAuthenticated: auth.isAuthenticated,
      isAdmin: auth.isAdmin,
      isStudent: auth.isStudent,
      isConsented: auth.isConsented,
      isExpired: auth.isExpired,
      mustChangePassword: auth.mustChangePassword,
      onboardingPrompted: auth.onboardingPrompted,
      pendingTwoFactor: auth.pendingTwoFactor,
      logout: auth.logout
    },
    {
      setupPromise: setup.getSetupPromise(),
      needsSetup: setup.needsSetup
    }
  )
  return redirect ?? true
})

export default router
