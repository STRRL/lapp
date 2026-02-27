package multiline

import "testing"

func TestDetectorTimestampLines(t *testing.T) {
	d, err := NewDetector(DetectorConfig{})
	if err != nil {
		t.Fatal(err)
	}

	timestamped := []string{
		"2024-03-28 13:45:30 INFO Application started",
		"2024-03-28T13:45:30.123456Z INFO Starting",
		"Mar 16 08:12:04 myhost sshd[1234]: Accepted",
		"28/Mar/2024:13:45:30 +0000 GET /api/health",
		"2024/04/25 14:57:42 [error] worker exited",
	}

	for _, line := range timestamped {
		if !d.IsNewEntry(line) {
			t.Errorf("expected IsNewEntry=true for %q", line)
		}
	}
}

func TestDetectorContinuationLines(t *testing.T) {
	d, err := NewDetector(DetectorConfig{})
	if err != nil {
		t.Fatal(err)
	}

	continuations := []string{
		"\tat com.example.Foo.bar(Foo.java:42)",
		"\tat sun.reflect.NativeMethodAccessorImpl.invoke(NativeMethodAccessorImpl.java:62)",
		"Caused by: java.lang.NullPointerException",
		"  File \"/app/worker.py\", line 45, in process_task",
		"    result = compute(data)",
		"ZeroDivisionError: division by zero",
		"goroutine 42 [running]:",
		"main.handleUsers(0xc000120000)",
		"\t/app/handlers.go:78 +0x1a4",
		"java.lang.NullPointerException: Cannot invoke method",
		"	... 2 more",
	}

	for _, line := range continuations {
		if d.IsNewEntry(line) {
			t.Errorf("expected IsNewEntry=false for %q", line)
		}
	}
}

func TestDetectorEmptyLine(t *testing.T) {
	d, err := NewDetector(DetectorConfig{})
	if err != nil {
		t.Fatal(err)
	}

	if d.IsNewEntry("") {
		t.Error("expected IsNewEntry=false for empty line")
	}
}

func TestDetectorFirstLineRegex(t *testing.T) {
	d, err := NewDetector(DetectorConfig{
		FirstLineRegex: `^\d{4}-\d{2}-\d{2}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !d.IsNewEntry("2024-03-28 something") {
		t.Error("expected regex match")
	}
	if d.IsNewEntry("\tat com.example.Foo.bar(Foo.java:42)") {
		t.Error("expected no regex match for continuation")
	}
}

func TestDetectorInvalidRegex(t *testing.T) {
	_, err := NewDetector(DetectorConfig{
		FirstLineRegex: `[invalid`,
	})
	if err == nil {
		t.Error("expected error for invalid regex")
	}
}
