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
			{Capability: "x11-image", ID: "capture-extent", Guidance: "XGetImage width and height must be the queried root-window dimensions; zero does not mean the whole window."},
			{Capability: "x11-image", ID: "channel-masks", Guidance: "XImage RGB masks are normally contiguous multi-bit fields, not powers of two: count trailing zeroes for shift, shift the mask down, then count its consecutive one bits for width and scale that field maximum to 0..255. Counting through the original mask includes the shift and is wrong."},
			{Capability: "x11-image", ID: "pixel-layout", Guidance: "XGetPixel already honors the source bits_per_pixel, bytes_per_line, and byte_order. A newly allocated normalized RGBA destination instead needs its own stride (normally width*4) and allocation size; do not reuse the source stride or cast source data to unsigned long*."},
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
			{Capability: "glib-async", ID: "task-lifetime", Guidance: "g_task_run_in_thread manages its worker reference. With a manual g_thread_new, pass g_object_ref(task) to the worker, g_object_unref it when the worker finishes, and immediately g_thread_unref the returned handle for detached execution; do not join from the caller's main context or treat a borrowed task pointer as owned."},
			{Capability: "glib-async", ID: "completion", Guidance: "Every success and error path must complete the async result exactly once, without making the caller's main context wait for the worker."},
			{Capability: "glib-async", ID: "nested-destroy", Guidance: "A GTask result destroy-notify must release nested owned allocations as well as the outer result object."},
		},
	}
}
