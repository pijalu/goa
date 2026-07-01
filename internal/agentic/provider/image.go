// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import "github.com/pijalu/goa/internal/agentic/provider/schema"

// ImageContent holds image data for content blocks.
type ImageContent = schema.ImageContent

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
