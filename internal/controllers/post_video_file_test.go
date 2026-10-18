package controllers

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestPostVideoRealFileDuration(t *testing.T) {
	if _, e := exec.LookPath("ffmpeg"); e != nil {
		t.Skip("ffmpeg not installed in local test environment")
	}
	if _, e := exec.LookPath("ffprobe"); e != nil {
		t.Skip("ffprobe not installed")
	}
	ctx, done := context.WithTimeout(context.Background(), 30*time.Second)
	defer done()
	for _, duration := range []string{"1", "61"} {
		name := filepath.Join(t.TempDir(), "test.mp4")
		raw, e := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=size=16x16:rate=1", "-t", duration, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-an", name).CombinedOutput()
		if e != nil {
			t.Fatalf("synthetic fixture failed %s: %v", raw, e)
		}
		_, e = probePostVideo(ctx, name)
		if duration == "1" && e != nil {
			t.Fatal(e)
		}
		if duration == "61" && e == nil {
			t.Fatal("real 61-second video accepted")
		}
	}
}
