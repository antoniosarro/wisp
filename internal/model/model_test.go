package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageJSONLeavesOutImages(t *testing.T) {
	data, err := json.Marshal(Message{Role: RoleTool, Images: []Image{{MIME: "image/png", Data: []byte("png")}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Images") || strings.Contains(string(data), "image/png") {
		t.Errorf("Marshal kept the images: %s", data)
	}
}
