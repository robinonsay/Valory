// @{"req": ["REQ-FECOURSE-561", "REQ-FECOURSE-562"]}
import { describe, it, expect } from "vitest";
import { humanizeGenerationEvent } from "./humanizeGenerationEvent";

describe("humanizeGenerationEvent", () => {
  describe("REQ-FECOURSE-071: Known event type mappings", () => {
    it("maps generation_started to a readable message", () => {
      const result = humanizeGenerationEvent(
        "generation_started",
        JSON.stringify({ payload: { course_id: "test-id" } }),
      );
      expect(result).toContain("Starting");
      expect(result).not.toContain("{");
      expect(result).not.toContain('"');
    });

    it("maps generation_complete to a readable message", () => {
      const result = humanizeGenerationEvent(
        "generation_complete",
        JSON.stringify({ payload: { course_id: "test-id" } }),
      );
      expect(result).toContain("complete");
      expect(result).not.toContain("{");
    });

    it("maps generation_timeout to a readable message", () => {
      const result = humanizeGenerationEvent(
        "generation_timeout",
        JSON.stringify({ payload: { course_id: "test-id" } }),
      );
      expect(result).toContain("timed out");
      expect(result).not.toContain("{");
    });

    it("maps api_failure with error message to a readable sentence", () => {
      const result = humanizeGenerationEvent(
        "api_failure",
        JSON.stringify({
          payload: { course_id: "test-id", error: "Rate limit exceeded" },
        }),
      );
      expect(result).toContain("failed");
      expect(result).toContain("Rate limit exceeded");
      expect(result).not.toContain("{");
    });

    it("maps api_failure without error message to a readable sentence", () => {
      const result = humanizeGenerationEvent(
        "api_failure",
        JSON.stringify({ payload: { course_id: "test-id" } }),
      );
      expect(result).toContain("failed");
      expect(result).not.toContain("{");
    });

    it("maps section_generating with title and 0-based index to readable message with 1-based display", () => {
      const result = humanizeGenerationEvent(
        "section_generating",
        JSON.stringify({
          payload: { section_index: 0, title: "Course Description" },
        }),
      );
      expect(result).toContain("Generating section 1: Course Description");
      expect(result).not.toContain("{");
      expect(result).not.toContain('"');
    });

    it("maps section_generating with higher section_index to 1-based display", () => {
      const result = humanizeGenerationEvent(
        "section_generating",
        JSON.stringify({
          payload: { section_index: 4, title: "Advanced Topics" },
        }),
      );
      expect(result).toContain("section 5");
      expect(result).toContain("Advanced Topics");
    });

    it("maps section_generating with missing title to a default", () => {
      const result = humanizeGenerationEvent(
        "section_generating",
        JSON.stringify({
          payload: { section_index: 2 },
        }),
      );
      expect(result).toContain("section 3");
      expect(result).toContain("Section");
      expect(result).not.toContain("{");
    });

    it("maps section_review_passed with section_index to readable message", () => {
      const result = humanizeGenerationEvent(
        "section_review_passed",
        JSON.stringify({
          payload: { section_index: 0, content_id: "content-123" },
        }),
      );
      expect(result).toMatch(/[Ss]ection 1/);
      expect(result).toContain("passed review");
      expect(result).not.toContain("{");
    });

    it("maps section_review_failed with section_index to readable message", () => {
      const result = humanizeGenerationEvent(
        "section_review_failed",
        JSON.stringify({
          payload: { section_index: 2, feedback: "Add more citations" },
        }),
      );
      expect(result).toMatch(/[Ss]ection 3/);
      expect(result).toContain("revision");
      expect(result).not.toContain("{");
    });

    it("maps correction_escalated with iterations to readable message", () => {
      const result = humanizeGenerationEvent(
        "correction_escalated",
        JSON.stringify({
          payload: {
            content_id: "content-123",
            iterations: 5,
            feedback: "Still has issues",
          },
        }),
      );
      expect(result).toContain("maximum attempts");
      expect(result).toContain("5");
      expect(result).toContain("admin review");
      expect(result).not.toContain("{");
    });

    it("maps section_regen_started with section_index to readable message", () => {
      const result = humanizeGenerationEvent(
        "section_regen_started",
        JSON.stringify({
          payload: { section_index: 1, feedback_id: "feedback-123" },
        }),
      );
      expect(result).toContain("section 2");
      expect(result).toContain("feedback");
      expect(result).not.toContain("{");
    });

    it("maps section_regen_complete with section_index to readable message", () => {
      const result = humanizeGenerationEvent(
        "section_regen_complete",
        JSON.stringify({
          payload: { section_index: 1 },
        }),
      );
      expect(result).toMatch(/[Ss]ection 2/);
      expect(result).toContain("complete");
      expect(result).not.toContain("{");
    });
  });

  describe("REQ-FECOURSE-072: Unknown event type fallback", () => {
    it("returns a non-JSON message for an unrecognized event type", () => {
      const result = humanizeGenerationEvent(
        "unknown_event_type",
        JSON.stringify({ payload: { some_field: "some_value" } }),
      );
      // Should not contain JSON indicators
      expect(result).not.toContain("{");
      expect(result).not.toContain('"');
      expect(result).not.toContain("event_type");
      expect(result).not.toContain("payload");
      // Should contain a humanized version of the event type
      expect(result).toContain("Unknown");
    });

    it("returns a humanized message for a hypothetical future event type", () => {
      const result = humanizeGenerationEvent(
        "section_review_escalated",
        JSON.stringify({ payload: { section_index: 0 } }),
      );
      // Convert snake_case to Title Case
      expect(result).toMatch(/Section.*Review.*Escalated/i);
      expect(result).not.toContain("{");
    });

    it("handles invalid JSON in the payload gracefully", () => {
      const result = humanizeGenerationEvent(
        "new_event_type",
        "not valid json",
      );
      expect(result).not.toContain("{");
      expect(result).not.toContain("not valid json");
      expect(result).toMatch(/New Event Type/i);
    });

    it("returns a readable message when the entire event is unparseable", () => {
      const result = humanizeGenerationEvent("mystery_event", "{]");
      // Should still produce a readable message without exposing the malformed JSON
      expect(result).not.toContain("{");
      expect(result).not.toContain("]");
      expect(result).toMatch(/Mystery/i);
    });
  });

  describe("Payload edge cases", () => {
    it("handles missing payload field gracefully", () => {
      const result = humanizeGenerationEvent(
        "section_generating",
        JSON.stringify({ section_index: 0, title: "Test" }),
      );
      expect(result).toContain("section 1");
      expect(result).toContain("Test");
    });

    it("handles null/undefined section_index by defaulting to 0", () => {
      const result = humanizeGenerationEvent(
        "section_generating",
        JSON.stringify({ payload: { title: "Test Section" } }),
      );
      expect(result).toContain("section 1");
    });

    it("handles very large section_index values", () => {
      const result = humanizeGenerationEvent(
        "section_generating",
        JSON.stringify({
          payload: { section_index: 99, title: "Final Section" },
        }),
      );
      expect(result).toContain("section 100");
    });
  });
});
