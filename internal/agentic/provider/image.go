// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// ImageContent holds image data for content blocks.
type ImageContent = schema.ImageContent

// EncodedImage is a resolved image attachment (Base64 payload + MIME type).
type EncodedImage = schema.EncodedImage

// EncodeImage resolves an image content source (filesystem path or data: URL)
// into Base64 payload + MIME type. ok=false when the source cannot be read or
// is not a recognisable image.
func EncodeImage(src string) (EncodedImage, bool) { return schema.EncodeImage(src) }

// ImageToDataURL returns a data: URL for an image source, or "" when it cannot
// be resolved.
func ImageToDataURL(src string) string { return schema.ImageToDataURL(src) }

// ImageUnavailablePlaceholder is emitted in place of an image block whose
// source could not be read.
const ImageUnavailablePlaceholder = schema.ImageUnavailablePlaceholder

// BedrockImageFormat maps a MIME type to the Bedrock Converse image format
// token. Bedrock's `format` field is a bare token ("png", "jpeg", …), not a
// MIME type, so the media type must be narrowed before it goes on the wire.
func BedrockImageFormat(mimeType string) string {
	switch mimeType {
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpeg"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	}
	return strings.TrimPrefix(mimeType, "image/")
}

// IsVisionModel returns true if the model supports image inputs.
func IsVisionModel(m Model) bool {
	if m.IsVisionModel {
		return true
	}
	for _, t := range m.InputTypes {
		if t == "image" {
			return true
		}
	}
	return false
}

// DowngradeImageBlock replaces an image content block with a text placeholder.
func DowngradeImageBlock(placeholder string) ContentBlock {
	return ContentBlock{
		Type: ContentBlockText,
		Text: placeholder,
	}
}

// downgradeImagesInContent replaces image content blocks with text placeholders.
func downgradeImagesInContent(content []ContentBlock, placeholder string) []ContentBlock {
	if len(content) == 0 {
		return content
	}

	result := make([]ContentBlock, 0, len(content))
	previousWasPlaceholder := false

	for _, block := range content {
		if block.Type == ContentBlockImage {
			if !previousWasPlaceholder {
				result = append(result, DowngradeImageBlock(placeholder))
			}
			previousWasPlaceholder = true
			continue
		}
		result = append(result, block)
		previousWasPlaceholder = block.Type == ContentBlockText && block.Text == placeholder
	}

	return result
}
