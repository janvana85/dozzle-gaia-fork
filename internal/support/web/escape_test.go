package support_web

import (
	"testing"

	"github.com/amir20/dozzle/internal/container"
	"github.com/stretchr/testify/assert"
)

func TestEscapeHTMLValuesEscapesTopLevelAnyMap(t *testing.T) {
	event := &container.LogEvent{
		Message: map[string]interface{}{
			"message": "<script>alert(1)</script>",
			"url":     "https://example.com/path?q=1",
			"nested": map[string]interface{}{
				"value": "<b>nested</b>",
			},
		},
	}

	EscapeHTMLValues(event)

	message := event.Message.(map[string]interface{})
	assert.Equal(t, "&lt;script&gt;alert(1)&lt;/script&gt;", message["message"])
	assert.Equal(t, `<a href="https://example.com/path?q=1" target="_blank" rel="noopener noreferrer external">https://example.com/path?q=1</a>`, message["url"])
	assert.Equal(t, "&lt;b&gt;nested&lt;/b&gt;", message["nested"].(map[string]interface{})["value"])
}

func TestEscapeHTMLValuesEscapesTopLevelStringMap(t *testing.T) {
	event := &container.LogEvent{
		Message: map[string]string{
			"message": "<b>hello</b>",
		},
	}

	EscapeHTMLValues(event)

	assert.Equal(t, "&lt;b&gt;hello&lt;/b&gt;", event.Message.(map[string]string)["message"])
}
