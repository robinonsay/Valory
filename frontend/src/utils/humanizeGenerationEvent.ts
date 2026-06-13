// @{"req": ["REQ-FECOURSE-071", "REQ-FECOURSE-072"]}

/**
 * Represents the structure of a pipeline event payload as emitted by the backend.
 */
interface GenerationEventPayload {
  [key: string]: any;
}

/**
 * Represents a parsed generation event with typed fields.
 */
interface ParsedGenerationEvent {
  eventType: string;
  payload: GenerationEventPayload;
}

/**
 * Humanizes a generation event into a readable English sentence.
 *
 * Known event types (REQ-FECOURSE-071) are mapped to specific human-readable
 * sentences incorporating relevant payload fields. Unknown event types
 * (REQ-FECOURSE-072) fall back to a generic message derived from the event type
 * (snake_case → Title Case) without exposing JSON.
 *
 * @param eventType - The event type string from the backend
 * @param dataStr - The raw JSON string payload from SSE
 * @returns A human-readable English sentence describing the event
 */
export function humanizeGenerationEvent(
  eventType: string,
  dataStr: string,
): string {
  let event: ParsedGenerationEvent;

  try {
    const parsed = JSON.parse(dataStr);
    event = {
      eventType,
      payload: parsed.payload || parsed,
    };
  } catch {
    // If the entire message fails to parse, return a generic fallback
    return `Processing… (${humanizeEventType(eventType)})`;
  }

  // REQ-FECOURSE-071: Map known event types to human-readable sentences
  switch (eventType) {
    case "generation_started":
      return "Starting course generation…";

    case "generation_complete":
      return "Course generation complete!";

    case "generation_timeout":
      return "Course generation timed out. Please contact support.";

    case "api_failure": {
      const error = event.payload.error || "API error";
      return `Generation failed: ${error}. Please try again later.`;
    }

    case "section_generating": {
      const title = event.payload.title || "Section";
      const sectionIndex = event.payload.section_index ?? 0;
      // REQ-FECOURSE-071: Convert 0-based index to 1-based display
      return `Generating section ${sectionIndex + 1}: ${title}…`;
    }

    case "section_review_passed": {
      const sectionIndex = event.payload.section_index ?? 0;
      return `Section ${sectionIndex + 1} passed review.`;
    }

    case "section_review_failed": {
      const sectionIndex = event.payload.section_index ?? 0;
      return `Section ${sectionIndex + 1} needs revision. Regenerating…`;
    }

    case "correction_escalated": {
      const iterations = event.payload.iterations || "multiple";
      return `Section revision reached maximum attempts (${iterations}). Escalated for admin review.`;
    }

    case "section_regen_started": {
      const sectionIndex = event.payload.section_index ?? 0;
      return `Regenerating section ${sectionIndex + 1} based on your feedback…`;
    }

    case "section_regen_complete": {
      const sectionIndex = event.payload.section_index ?? 0;
      return `Section ${sectionIndex + 1} regeneration complete.`;
    }

    // REQ-FECOURSE-072: Graceful fallback for unknown event types
    // Never display raw JSON; derive a readable message from the event_type
    default:
      return `${humanizeEventType(eventType)}…`;
  }
}

/**
 * Converts a snake_case event type to a Title Case readable string.
 * Used as the fallback message for unknown event types (REQ-FECOURSE-072).
 *
 * Examples:
 *   "section_generating" → "Section Generating"
 *   "api_failure" → "Api Failure"
 *   "unknown_event" → "Unknown Event"
 *
 * @param eventType - The snake_case event type
 * @returns A humanized, title-cased string
 */
function humanizeEventType(eventType: string): string {
  return eventType
    .split("_")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(" ");
}
