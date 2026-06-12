// vision.go — helpers for building Anthropic vision content blocks from image rows.
// Used by both the chat handler (REQ-AGENT-023) and the grading runner (REQ-AGENT-025).
package image

import (
	"encoding/base64"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// ImageToBlock converts image bytes and MIME type to an Anthropic
// ContentBlockParamUnion carrying a base64-encoded image source.
// The base64 source type is the only viable option for locally-stored images
// because Claude's servers cannot reach the private same-host endpoint.
//
// @{"req": ["REQ-AGENT-023", "REQ-AGENT-025"]}
func ImageToBlock(mimeType string, data []byte) anthropic.ContentBlockParamUnion {
	return anthropic.NewImageBlockBase64(mimeType, base64.StdEncoding.EncodeToString(data))
}

// BuildCurrentTurnContent constructs the content block slice for the current
// student turn. Image blocks precede the text block, matching the Anthropic
// recommendation for vision prompts. When images is empty the result is a
// single text block — identical to the existing non-vision path.
//
// History turns (from buildMessages) remain single-block text params; only
// the current turn carries image blocks. This bounds token cost to exactly one
// image set per call regardless of conversation length.
//
// @{"req": ["REQ-AGENT-023"]}
func BuildCurrentTurnContent(message string, images []anthropic.ContentBlockParamUnion) []anthropic.ContentBlockParamUnion {
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(images)+1)
	blocks = append(blocks, images...)
	blocks = append(blocks, anthropic.NewTextBlock(message))
	return blocks
}

// ImageRowsToBlocks converts a slice of ImageRow values to ContentBlockParamUnion
// image blocks. The caller is responsible for slicing to the required cap before
// calling (4 for chat, 8 for grading).
//
// @{"req": ["REQ-AGENT-023", "REQ-AGENT-025"]}
func ImageRowsToBlocks(rows []ImageRow) []anthropic.ContentBlockParamUnion {
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(rows))
	for _, row := range rows {
		blocks = append(blocks, ImageToBlock(row.MimeType, row.Data))
	}
	return blocks
}
