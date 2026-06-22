package tools

import (
	"context"
	"fmt"

	"charm.land/fantasy"
)

// VisionClient describes images using a configured vision model. When nil,
// vision delegation is not available and image files are handled directly
// (or rejected for non-vision models).
type VisionClient struct {
	describer func(ctx context.Context, imageData []byte, mimeType string) (string, error)
}

// NewVisionClient creates a VisionClient that uses the given language model
// to describe images.
func NewVisionClient(model fantasy.LanguageModel) *VisionClient {
	return &VisionClient{
		describer: func(ctx context.Context, imageData []byte, mimeType string) (string, error) {
			agent := fantasy.NewAgent(model)
			result, err := agent.Generate(ctx, fantasy.AgentCall{
				Messages: []fantasy.Message{{
					Role: fantasy.MessageRoleUser,
					Content: []fantasy.MessagePart{
						fantasy.TextPart{Text: "Describe this image in detail. Focus on the content, layout, and any text visible in the image."},
						fantasy.FilePart{
							Filename:  "image",
							Data:      imageData,
							MediaType: mimeType,
						},
					},
				}},
			})
			if err != nil {
				return "", fmt.Errorf("vision model generation failed: %w", err)
			}
			var text string
			for _, c := range result.Response.Content {
				if c.GetType() == fantasy.ContentTypeText {
					if tc, ok := fantasy.AsContentType[fantasy.TextContent](c); ok {
						text += tc.Text
					}
				}
			}
			if text == "" {
				return "", fmt.Errorf("vision model returned empty response")
			}
			return text, nil
		},
	}
}

// DescribeImage sends the image to the vision model and returns a text
// description. Returns nil, nil if the client is nil (no vision model
// configured).
func (v *VisionClient) DescribeImage(ctx context.Context, imageData []byte, mimeType string) (string, error) {
	if v == nil {
		return "", nil
	}
	return v.describer(ctx, imageData, mimeType)
}
