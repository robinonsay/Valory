// @{"req": ["REQ-FECOURSE-065", "REQ-FECOURSE-066", "REQ-FECOURSE-067", "REQ-FECOURSE-070", "REQ-FECOURSE-071", "REQ-FECOURSE-072", "REQ-FECOURSE-073", "REQ-FECOURSE-074", "REQ-FECOURSE-530", "REQ-FECOURSE-540", "REQ-FECOURSE-550", "REQ-FECOURSE-560", "REQ-FECOURSE-561", "REQ-FECOURSE-562"]}
import { describe, it, expect, beforeEach, vi } from "vitest";
import { mount } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";
import { createRouter, createMemoryHistory } from "vue-router";
import GeneratingView from "./GeneratingView.vue";
import { useAuthStore } from "@/stores/auth";
import * as useSSEModule from "@/composables/useSSE";

const mockRouterPush = vi.fn();

const router = createRouter({
  history: createMemoryHistory(),
  routes: [
    {
      path: "/courses/:id/generating",
      component: GeneratingView,
    },
    {
      path: "/courses/:id/hub",
      component: { template: "<div>Hub</div>" },
    },
  ],
});

describe("GeneratingView", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    mockRouterPush.mockClear();
  });

  it("connects to SSE on mount", async () => {
    const mockUseSSE = vi.fn().mockReturnValue({ close: vi.fn() });
    vi.spyOn(useSSEModule, "useSSE").mockImplementation(mockUseSSE);

    const auth = useAuthStore();
    auth.$patch({
      role: "student",
      expiresAt: Math.floor(Date.now() / 1000) + 3600,
      restoreDone: true,
    });

    router.push("/courses/test-course-id/generating");
    await router.isReady();

    const wrapper = mount(GeneratingView, {
      global: {
        plugins: [router],
      },
    });

    await wrapper.vm.$nextTick();

    expect(mockUseSSE).toHaveBeenCalled();
    const callArgs = mockUseSSE.mock.calls[0];
    expect(callArgs[0]).toBe("/api/v1/courses/test-course-id/events");
    // Cookie-based auth: token is not passed; SSE uses __Host-session cookie automatically
    expect(callArgs[1].token == null).toBe(true);
  });

  it("updates progress percentage on progress event", async () => {
    let capturedOnEvent: ((eventType: string, data: string) => void) | null =
      null;

    const mockClose = vi.fn();
    vi.spyOn(useSSEModule, "useSSE").mockImplementation((_url, options) => {
      capturedOnEvent = options.onEvent;
      return { close: mockClose };
    });

    const auth = useAuthStore();
    auth.$patch({
      role: "student",
      expiresAt: Math.floor(Date.now() / 1000) + 3600,
      restoreDone: true,
    });

    router.push("/courses/test-course-id/generating");
    await router.isReady();

    const wrapper = mount(GeneratingView, {
      global: {
        plugins: [router],
      },
    });

    await wrapper.vm.$nextTick();

    expect(capturedOnEvent).not.toBeNull();
    capturedOnEvent!(
      "progress",
      JSON.stringify({ percent: 50, message: "Generating content" }),
    );
    await wrapper.vm.$nextTick();

    expect(wrapper.vm.progressPercent).toBe(50);
    expect(wrapper.text()).toContain("Generating content");
  });

  it("navigates to hub when status_change event with active status is received", async () => {
    let capturedOnEvent: ((eventType: string, data: string) => void) | null =
      null;

    const mockClose = vi.fn();
    vi.spyOn(useSSEModule, "useSSE").mockImplementation((_url, options) => {
      capturedOnEvent = options.onEvent;
      return { close: mockClose };
    });

    const auth = useAuthStore();
    auth.$patch({
      role: "student",
      expiresAt: Math.floor(Date.now() / 1000) + 3600,
      restoreDone: true,
    });

    router.push("/courses/test-course-id/generating");
    await router.isReady();

    const routerPushSpy = vi.spyOn(router, "push");

    const wrapper = mount(GeneratingView, {
      global: {
        plugins: [router],
      },
    });

    await wrapper.vm.$nextTick();

    expect(capturedOnEvent).not.toBeNull();
    capturedOnEvent!("status_change", JSON.stringify({ status: "active" }));
    await wrapper.vm.$nextTick();

    expect(routerPushSpy).toHaveBeenCalledWith("/courses/test-course-id/hub");
  });

  it("calls sse.close() on unmount", async () => {
    const mockClose = vi.fn();
    vi.spyOn(useSSEModule, "useSSE").mockReturnValue({ close: mockClose });

    const auth = useAuthStore();
    auth.$patch({
      role: "student",
      expiresAt: Math.floor(Date.now() / 1000) + 3600,
      restoreDone: true,
    });

    router.push("/courses/test-course-id/generating");
    await router.isReady();

    const wrapper = mount(GeneratingView, {
      global: {
        plugins: [router],
      },
    });

    await wrapper.vm.$nextTick();

    wrapper.unmount();
    await wrapper.vm.$nextTick();

    expect(mockClose).toHaveBeenCalled();
  });

  it("displays error message when SSE error occurs", async () => {
    let capturedOnError: ((err: Error) => void) | null = null;

    const mockClose = vi.fn();
    vi.spyOn(useSSEModule, "useSSE").mockImplementation((_url, options) => {
      capturedOnError = options.onError;
      return { close: mockClose };
    });

    const auth = useAuthStore();
    auth.$patch({
      role: "student",
      expiresAt: Math.floor(Date.now() / 1000) + 3600,
      restoreDone: true,
    });

    router.push("/courses/test-course-id/generating");
    await router.isReady();

    const wrapper = mount(GeneratingView, {
      global: {
        plugins: [router],
      },
    });

    await wrapper.vm.$nextTick();

    expect(capturedOnError).not.toBeNull();
    capturedOnError!(new Error("Connection failed"));
    await wrapper.vm.$nextTick();

    expect(wrapper.text()).toContain(
      "Generation failed. Please contact support.",
    );
  });

  describe("REQ-FECOURSE-071: Known event type humanization", () => {
    it("renders generation_started as human-readable message", async () => {
      let capturedOnEvent: ((eventType: string, data: string) => void) | null =
        null;

      vi.spyOn(useSSEModule, "useSSE").mockImplementation((_url, options) => {
        capturedOnEvent = options.onEvent;
        return { close: vi.fn() };
      });

      const auth = useAuthStore();
      auth.$patch({
        role: "student",
        expiresAt: Math.floor(Date.now() / 1000) + 3600,
        restoreDone: true,
      });

      router.push("/courses/test-course-id/generating");
      await router.isReady();

      const wrapper = mount(GeneratingView, {
        global: {
          plugins: [router],
        },
      });

      await wrapper.vm.$nextTick();

      capturedOnEvent!(
        "generation_started",
        JSON.stringify({ payload: { course_id: "test-id" } }),
      );
      await wrapper.vm.$nextTick();

      expect(wrapper.text()).toContain("Starting");
      expect(wrapper.text()).not.toContain("{");
      expect(wrapper.text()).not.toContain("event_type");
    });

    it("renders section_generating with section index as 1-based and title", async () => {
      let capturedOnEvent: ((eventType: string, data: string) => void) | null =
        null;

      vi.spyOn(useSSEModule, "useSSE").mockImplementation((_url, options) => {
        capturedOnEvent = options.onEvent;
        return { close: vi.fn() };
      });

      const auth = useAuthStore();
      auth.$patch({
        role: "student",
        expiresAt: Math.floor(Date.now() / 1000) + 3600,
        restoreDone: true,
      });

      router.push("/courses/test-course-id/generating");
      await router.isReady();

      const wrapper = mount(GeneratingView, {
        global: {
          plugins: [router],
        },
      });

      await wrapper.vm.$nextTick();

      capturedOnEvent!(
        "section_generating",
        JSON.stringify({
          payload: { section_index: 0, title: "Course Description" },
        }),
      );
      await wrapper.vm.$nextTick();

      expect(wrapper.text()).toContain("section 1");
      expect(wrapper.text()).toContain("Course Description");
      expect(wrapper.text()).not.toContain("{");
    });

    it("renders section_review_passed as human-readable message", async () => {
      let capturedOnEvent: ((eventType: string, data: string) => void) | null =
        null;

      vi.spyOn(useSSEModule, "useSSE").mockImplementation((_url, options) => {
        capturedOnEvent = options.onEvent;
        return { close: vi.fn() };
      });

      const auth = useAuthStore();
      auth.$patch({
        role: "student",
        expiresAt: Math.floor(Date.now() / 1000) + 3600,
        restoreDone: true,
      });

      router.push("/courses/test-course-id/generating");
      await router.isReady();

      const wrapper = mount(GeneratingView, {
        global: {
          plugins: [router],
        },
      });

      await wrapper.vm.$nextTick();

      capturedOnEvent!(
        "section_review_passed",
        JSON.stringify({
          payload: { section_index: 0, content_id: "content-123" },
        }),
      );
      await wrapper.vm.$nextTick();

      expect(wrapper.text()).toContain("passed review");
      expect(wrapper.text()).not.toContain("{");
    });

    it("renders section_review_failed as human-readable message", async () => {
      let capturedOnEvent: ((eventType: string, data: string) => void) | null =
        null;

      vi.spyOn(useSSEModule, "useSSE").mockImplementation((_url, options) => {
        capturedOnEvent = options.onEvent;
        return { close: vi.fn() };
      });

      const auth = useAuthStore();
      auth.$patch({
        role: "student",
        expiresAt: Math.floor(Date.now() / 1000) + 3600,
        restoreDone: true,
      });

      router.push("/courses/test-course-id/generating");
      await router.isReady();

      const wrapper = mount(GeneratingView, {
        global: {
          plugins: [router],
        },
      });

      await wrapper.vm.$nextTick();

      capturedOnEvent!(
        "section_review_failed",
        JSON.stringify({
          payload: { section_index: 2, feedback: "Add more citations" },
        }),
      );
      await wrapper.vm.$nextTick();

      expect(wrapper.text()).toMatch(/[Ss]ection 3/);
      expect(wrapper.text()).toContain("revision");
      expect(wrapper.text()).not.toContain("{");
    });

    it("renders correction_escalated as human-readable message with iteration count", async () => {
      let capturedOnEvent: ((eventType: string, data: string) => void) | null =
        null;

      vi.spyOn(useSSEModule, "useSSE").mockImplementation((_url, options) => {
        capturedOnEvent = options.onEvent;
        return { close: vi.fn() };
      });

      const auth = useAuthStore();
      auth.$patch({
        role: "student",
        expiresAt: Math.floor(Date.now() / 1000) + 3600,
        restoreDone: true,
      });

      router.push("/courses/test-course-id/generating");
      await router.isReady();

      const wrapper = mount(GeneratingView, {
        global: {
          plugins: [router],
        },
      });

      await wrapper.vm.$nextTick();

      capturedOnEvent!(
        "correction_escalated",
        JSON.stringify({
          payload: {
            content_id: "content-123",
            iterations: 5,
            feedback: "Still has issues",
          },
        }),
      );
      await wrapper.vm.$nextTick();

      expect(wrapper.text()).toContain("maximum attempts");
      expect(wrapper.text()).toContain("5");
      expect(wrapper.text()).not.toContain("{");
    });

    it("renders generation_timeout as human-readable message", async () => {
      let capturedOnEvent: ((eventType: string, data: string) => void) | null =
        null;

      vi.spyOn(useSSEModule, "useSSE").mockImplementation((_url, options) => {
        capturedOnEvent = options.onEvent;
        return { close: vi.fn() };
      });

      const auth = useAuthStore();
      auth.$patch({
        role: "student",
        expiresAt: Math.floor(Date.now() / 1000) + 3600,
        restoreDone: true,
      });

      router.push("/courses/test-course-id/generating");
      await router.isReady();

      const wrapper = mount(GeneratingView, {
        global: {
          plugins: [router],
        },
      });

      await wrapper.vm.$nextTick();

      capturedOnEvent!(
        "generation_timeout",
        JSON.stringify({
          payload: { course_id: "test-id" },
        }),
      );
      await wrapper.vm.$nextTick();

      expect(wrapper.text()).toContain("timed out");
      expect(wrapper.text()).not.toContain("{");
    });

    it("renders api_failure with error message", async () => {
      let capturedOnEvent: ((eventType: string, data: string) => void) | null =
        null;

      vi.spyOn(useSSEModule, "useSSE").mockImplementation((_url, options) => {
        capturedOnEvent = options.onEvent;
        return { close: vi.fn() };
      });

      const auth = useAuthStore();
      auth.$patch({
        role: "student",
        expiresAt: Math.floor(Date.now() / 1000) + 3600,
        restoreDone: true,
      });

      router.push("/courses/test-course-id/generating");
      await router.isReady();

      const wrapper = mount(GeneratingView, {
        global: {
          plugins: [router],
        },
      });

      await wrapper.vm.$nextTick();

      capturedOnEvent!(
        "api_failure",
        JSON.stringify({
          payload: { course_id: "test-id", error: "Rate limit exceeded" },
        }),
      );
      await wrapper.vm.$nextTick();

      expect(wrapper.text()).toContain("failed");
      expect(wrapper.text()).toContain("Rate limit exceeded");
      expect(wrapper.text()).not.toContain("{");
    });

    it("renders section_regen_started as human-readable message", async () => {
      let capturedOnEvent: ((eventType: string, data: string) => void) | null =
        null;

      vi.spyOn(useSSEModule, "useSSE").mockImplementation((_url, options) => {
        capturedOnEvent = options.onEvent;
        return { close: vi.fn() };
      });

      const auth = useAuthStore();
      auth.$patch({
        role: "student",
        expiresAt: Math.floor(Date.now() / 1000) + 3600,
        restoreDone: true,
      });

      router.push("/courses/test-course-id/generating");
      await router.isReady();

      const wrapper = mount(GeneratingView, {
        global: {
          plugins: [router],
        },
      });

      await wrapper.vm.$nextTick();

      capturedOnEvent!(
        "section_regen_started",
        JSON.stringify({
          payload: { section_index: 1, feedback_id: "feedback-123" },
        }),
      );
      await wrapper.vm.$nextTick();

      expect(wrapper.text()).toContain("section 2");
      expect(wrapper.text()).toContain("feedback");
      expect(wrapper.text()).not.toContain("{");
    });

    it("renders section_regen_complete as human-readable message", async () => {
      let capturedOnEvent: ((eventType: string, data: string) => void) | null =
        null;

      vi.spyOn(useSSEModule, "useSSE").mockImplementation((_url, options) => {
        capturedOnEvent = options.onEvent;
        return { close: vi.fn() };
      });

      const auth = useAuthStore();
      auth.$patch({
        role: "student",
        expiresAt: Math.floor(Date.now() / 1000) + 3600,
        restoreDone: true,
      });

      router.push("/courses/test-course-id/generating");
      await router.isReady();

      const wrapper = mount(GeneratingView, {
        global: {
          plugins: [router],
        },
      });

      await wrapper.vm.$nextTick();

      capturedOnEvent!(
        "section_regen_complete",
        JSON.stringify({
          payload: { section_index: 1 },
        }),
      );
      await wrapper.vm.$nextTick();

      expect(wrapper.text()).toMatch(/[Ss]ection 2/);
      expect(wrapper.text()).toContain("complete");
      expect(wrapper.text()).not.toContain("{");
    });

    it("renders generation_complete as human-readable message", async () => {
      let capturedOnEvent: ((eventType: string, data: string) => void) | null =
        null;

      vi.spyOn(useSSEModule, "useSSE").mockImplementation((_url, options) => {
        capturedOnEvent = options.onEvent;
        return { close: vi.fn() };
      });

      const auth = useAuthStore();
      auth.$patch({
        role: "student",
        expiresAt: Math.floor(Date.now() / 1000) + 3600,
        restoreDone: true,
      });

      router.push("/courses/test-course-id/generating");
      await router.isReady();

      const wrapper = mount(GeneratingView, {
        global: {
          plugins: [router],
        },
      });

      await wrapper.vm.$nextTick();

      capturedOnEvent!(
        "generation_complete",
        JSON.stringify({
          payload: { course_id: "test-id" },
        }),
      );
      await wrapper.vm.$nextTick();

      expect(wrapper.text()).toContain("complete");
      expect(wrapper.text()).not.toContain("{");
    });
  });

  describe("REQ-FECOURSE-562: Unknown event type fallback", () => {
    it("renders unknown event types as non-JSON readable messages", async () => {
      let capturedOnEvent: ((eventType: string, data: string) => void) | null =
        null;

      vi.spyOn(useSSEModule, "useSSE").mockImplementation((_url, options) => {
        capturedOnEvent = options.onEvent;
        return { close: vi.fn() };
      });

      const auth = useAuthStore();
      auth.$patch({
        role: "student",
        expiresAt: Math.floor(Date.now() / 1000) + 3600,
        restoreDone: true,
      });

      router.push("/courses/test-course-id/generating");
      await router.isReady();

      const wrapper = mount(GeneratingView, {
        global: {
          plugins: [router],
        },
      });

      await wrapper.vm.$nextTick();

      capturedOnEvent!(
        "hypothetical_future_event",
        JSON.stringify({
          payload: { some_field: "some_value" },
        }),
      );
      await wrapper.vm.$nextTick();

      const displayedText = wrapper.text();
      // Must not contain raw JSON indicators
      expect(displayedText).not.toContain("{");
      expect(displayedText).not.toContain('"');
      expect(displayedText).not.toContain("event_type");
      expect(displayedText).not.toContain("payload");
      // Should contain a humanized version of the event type
      expect(displayedText).toMatch(/Hypothetical.*Future.*Event/i);
    });

    it("handles malformed JSON gracefully without displaying raw data", async () => {
      let capturedOnEvent: ((eventType: string, data: string) => void) | null =
        null;

      vi.spyOn(useSSEModule, "useSSE").mockImplementation((_url, options) => {
        capturedOnEvent = options.onEvent;
        return { close: vi.fn() };
      });

      const auth = useAuthStore();
      auth.$patch({
        role: "student",
        expiresAt: Math.floor(Date.now() / 1000) + 3600,
        restoreDone: true,
      });

      router.push("/courses/test-course-id/generating");
      await router.isReady();

      const wrapper = mount(GeneratingView, {
        global: {
          plugins: [router],
        },
      });

      await wrapper.vm.$nextTick();

      capturedOnEvent!("new_event", "not valid json");
      await wrapper.vm.$nextTick();

      const displayedText = wrapper.text();
      expect(displayedText).not.toContain("not valid json");
      expect(displayedText).not.toContain("{");
      expect(displayedText).not.toContain("[");
    });
  });

  describe("REQ-FECOURSE-073 & REQ-FECOURSE-074: Existing behavior preserved", () => {
    it("preserves progress bar update on progress event", async () => {
      let capturedOnEvent: ((eventType: string, data: string) => void) | null =
        null;

      vi.spyOn(useSSEModule, "useSSE").mockImplementation((_url, options) => {
        capturedOnEvent = options.onEvent;
        return { close: vi.fn() };
      });

      const auth = useAuthStore();
      auth.$patch({
        role: "student",
        expiresAt: Math.floor(Date.now() / 1000) + 3600,
        restoreDone: true,
      });

      router.push("/courses/test-course-id/generating");
      await router.isReady();

      const wrapper = mount(GeneratingView, {
        global: {
          plugins: [router],
        },
      });

      await wrapper.vm.$nextTick();

      capturedOnEvent!(
        "progress",
        JSON.stringify({ percent: 75, message: "Three-quarters done" }),
      );
      await wrapper.vm.$nextTick();

      expect(wrapper.vm.progressPercent).toBe(75);
      expect(wrapper.text()).toContain("75%");
      expect(wrapper.text()).toContain("Three-quarters done");
    });

    it("preserves navigation to course hub on status_change with active status", async () => {
      let capturedOnEvent: ((eventType: string, data: string) => void) | null =
        null;

      vi.spyOn(useSSEModule, "useSSE").mockImplementation((_url, options) => {
        capturedOnEvent = options.onEvent;
        return { close: vi.fn() };
      });

      const auth = useAuthStore();
      auth.$patch({
        role: "student",
        expiresAt: Math.floor(Date.now() / 1000) + 3600,
        restoreDone: true,
      });

      router.push("/courses/test-course-id/generating");
      await router.isReady();

      const routerPushSpy = vi.spyOn(router, "push");

      const wrapper = mount(GeneratingView, {
        global: {
          plugins: [router],
        },
      });

      await wrapper.vm.$nextTick();

      capturedOnEvent!("status_change", JSON.stringify({ status: "active" }));
      await wrapper.vm.$nextTick();

      expect(routerPushSpy).toHaveBeenCalledWith("/courses/test-course-id/hub");
    });
  });
});
