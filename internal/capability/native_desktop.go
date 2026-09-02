package capability

import "strings"

// X11Image contributes the representation invariants needed when a task uses
// XImage. It is deliberately independent from any project or task name.
type X11Image struct{}

func (X11Image) ID() string { return "x11-image" }

func (X11Image) Detect(ctx Context) Contributions {
	verification := strings.ToLower(ctx.TaskVerification)
	if !strings.Contains(verification, "x11") && !strings.Contains(verification, "xfixes") {
		return Contributions{}
	}
	return Contributions{
		Detection: Detection{Capability: "x11-image", Evidence: []string{"task Verification uses X11/XFixes"}},
		ReviewRules: []ReviewRule{
			{Capability: "x11-image", ID: "channel-masks", Guidance: "XImage red/green/blue masks are normally contiguous multi-bit fields, not powers of two; derive each shift and field width, then scale the extracted field maximum to 0..255."},
			{Capability: "x11-image", ID: "pixel-layout", Guidance: "Read XImage pixels with XGetPixel or explicitly honor bits_per_pixel, bytes_per_line, and byte_order; never infer the storage width by casting data to unsigned long*."},
			{Capability: "x11-image", ID: "alpha", Guidance: "Treat root-window captures as opaque unless the source format explicitly supplies a valid alpha channel."},
		},
	}
}

// GLibAsync contributes ownership and completion invariants for asynchronous
// GLib work. The rules compose with X11Image when a task uses both stacks.
type GLibAsync struct{}

func (GLibAsync) ID() string { return "glib-async" }

func (GLibAsync) Detect(ctx Context) Contributions {
	verification := strings.ToLower(ctx.TaskVerification)
	if !strings.Contains(verification, "glib-2.0") && !strings.Contains(verification, "gio-2.0") && !strings.Contains(verification, "gtk") {
		return Contributions{}
	}
	return Contributions{
		Detection: Detection{Capability: "glib-async", Evidence: []string{"task Verification uses GLib/GIO/GTK"}},
		ReviewRules: []ReviewRule{
			{Capability: "glib-async", ID: "task-lifetime", Guidance: "g_task_run_in_thread manages the worker reference; a manually created GThread instead needs explicit task ownership and thread-handle cleanup. Do not report one model as if it were the other."},
			{Capability: "glib-async", ID: "completion", Guidance: "Every success and error path must complete the async result exactly once, without making the caller's main context wait for the worker."},
			{Capability: "glib-async", ID: "nested-destroy", Guidance: "A GTask result destroy-notify must release nested owned allocations as well as the outer result object."},
		},
	}
}
