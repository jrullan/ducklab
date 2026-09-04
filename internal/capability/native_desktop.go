package capability

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// GTK4UI owns planning and review facts for GTK4 windows, input controllers,
// application lifecycle, drawing, and native file dialogs. Clipboard remains
// a separate capability because tasks should receive only relevant knowledge.
type GTK4UI struct{}

var gtk4RemovedWindowAPIs = []string{
	"gdk_window_type_hint", "gtk_window_set_keep_above", "gtk_widget_add_events",
	"gdk_window_set_events", "gdk_pointer_motion_mask", "gdk_button_press_mask",
	"gdk_button_release_mask", "gdk_key_press_mask",
}

func (GTK4UI) ID() string { return "gtk4-ui" }

func (GTK4UI) Detect(ctx Context) Contributions {
	if !strings.Contains(strings.ToLower(ctx.TaskVerification), "gtk4") {
		return Contributions{}
	}
	return Contributions{
		Detection: Detection{Capability: "gtk4-ui", Evidence: []string{"task Verification compiles code against GTK4"}},
		ReviewRules: []ReviewRule{
			{Capability: "gtk4-ui", ID: "window", Guidance: "GTK4 application windows are GtkWindow widgets. Use gtk_window_fullscreen (or fullscreen_on_monitor where required). GTK3 GdkWindow type hints, gtk_window_set_keep_above, gtk_widget_add_events, gdk_window_set_events, and GDK_*_MASK event masks are not GTK4 APIs."},
			{Capability: "gtk4-ui", ID: "input", Guidance: "GTK4 input uses controllers attached with gtk_widget_add_controller: GtkGestureClick for button press/release, GtkEventControllerMotion for motion, and GtkEventControllerKey for keys. GtkEventControllerButton and gtk_window_add_shortcut do not exist."},
			{Capability: "gtk4-ui", ID: "drawing", Guidance: "Configure GtkDrawingArea rendering with gtk_drawing_area_set_draw_func; GTK4 removed the GtkWidget draw signal and gdk_window_begin_draw_frame workflow."},
			{Capability: "gtk4-ui", ID: "lifecycle", Guidance: "Run a GtkApplication with g_application_run and quit it with g_application_quit. gtk_main, gtk_main_quit, and a standalone gtk_init-driven main loop are GTK3 contracts."},
			{Capability: "gtk4-ui", ID: "native-dialog", Guidance: "GtkFileChooserNative inherits GtkNativeDialog's response signal; accept only when response_id == GTK_RESPONSE_ACCEPT. There is no accept or file-confirmed signal."},
			{Capability: "gtk4-ui", ID: "async-file", Guidance: "g_file_replace_contents is synchronous. For a GBytes payload use g_file_replace_contents_bytes_async and complete with g_file_replace_contents_finish; MIME type is not an argument to this file-write API."},
		},
	}
}

func (GTK4UI) InspectPlanTask(ctx PlanTaskContext) []Inspection {
	text := strings.ToLower(ctx.Body + "\n" + ctx.Verification)
	// Section-sized tasks sometimes omit the literal word GTK4 while naming
	// its Gtk/Gdk symbols. Those symbols are enough stack evidence; requiring
	// the version token let a removed drawing API pass planning unnoticed.
	if !strings.Contains(text, "gtk4") && !strings.Contains(text, "gtk_") &&
		!strings.Contains(text, "gtkw") && !strings.Contains(text, "gtkg") &&
		!strings.Contains(text, "gdkw") && !strings.Contains(text, "gdk_") {
		return nil
	}
	invalid := []struct {
		name    string
		needles []string
		detail  string
	}{
		{"removed-window-api", gtk4RemovedWindowAPIs, "task specifies a GTK3 window/event API removed from GTK4; use GtkWindow fullscreen plus GtkGestureClick/GtkEventControllerMotion/GtkEventControllerKey attached with gtk_widget_add_controller"},
		{"invented-controller", []string{"gtkeventcontrollerbutton", "gtk_window_add_shortcut"}, "task specifies a non-existent GTK4 controller/shortcut API; button gestures use GtkGestureClick and keyboard input uses GtkEventControllerKey"},
		{"removed-drawing-api", []string{"gdk_window_begin_draw_frame", "`draw` signal", "\"draw\" signal"}, "task specifies GTK3 drawing; GTK4 GtkDrawingArea uses gtk_drawing_area_set_draw_func"},
		{"removed-main-loop", []string{"gtk_main()", "gtk_main_quit", "gtk_init()"}, "task specifies GTK3 main-loop lifecycle; GTK4 applications use g_application_run and g_application_quit"},
		{"invented-file-signal", []string{"\"accept\" signal", "`accept` signal", "file-confirmed"}, "GtkFileChooserNative reports GtkNativeDialog::response; check response_id == GTK_RESPONSE_ACCEPT"},
	}
	var out []Inspection
	for _, rule := range invalid {
		for _, needle := range rule.needles {
			if strings.Contains(text, needle) {
				out = append(out, Inspection{Capability: "gtk4-ui", Name: rule.name, Enforcement: Required, Detail: rule.detail})
				break
			}
		}
	}
	if strings.Contains(text, "g_file_replace_contents()") && strings.Contains(text, "async") {
		out = append(out, Inspection{Capability: "gtk4-ui", Name: "sync-file-api", Enforcement: Required, Detail: "task describes g_file_replace_contents as asynchronous; use g_file_replace_contents_bytes_async for GBytes and finish with g_file_replace_contents_finish"})
	}
	return out
}

func (GTK4UI) InspectReviewFindings(findings []ReviewFinding) []ReviewFindingInspection {
	var out []ReviewFindingInspection
	for i, finding := range findings {
		fix := strings.ToLower(strings.TrimSpace(finding.Fix))
		for _, api := range gtk4RemovedWindowAPIs {
			if !strings.Contains(fix, api) || remedyRejectsAPI(fix, api) {
				continue
			}
			out = append(out, ReviewFindingInspection{Index: i, Inspection: Inspection{
				Capability: "gtk4-ui", Name: "invalid-review-remedy", Enforcement: Required,
				Detail: fmt.Sprintf("finding %d is inadmissible: its fix proposes %s, a GTK3 API removed from GTK4", i, api),
			}})
			break
		}
	}
	return out
}

func remedyRejectsAPI(fix, api string) bool {
	idx := strings.Index(fix, api)
	if idx < 0 {
		return false
	}
	prefix := fix[:idx]
	if len(prefix) > 80 {
		prefix = prefix[len(prefix)-80:]
	}
	for _, rejection := range []string{"remove ", "delete ", "avoid ", "do not use ", "don't use ", "must not use ", "replace ", "instead of "} {
		if strings.Contains(prefix, rejection) {
			return true
		}
	}
	return false
}

// GTK4Clipboard contributes the GTK4 clipboard contract only when the task's
// verification names both GTK4 and clipboard code. It deliberately does not
// turn every GTK project into a clipboard project.
type GTK4Clipboard struct{}

func (GTK4Clipboard) ID() string { return "gtk4-clipboard" }

func (GTK4Clipboard) Detect(ctx Context) Contributions {
	verification := strings.ToLower(ctx.TaskVerification)
	if !strings.Contains(verification, "gtk4") || !strings.Contains(verification, "clipboard") {
		return Contributions{}
	}
	return Contributions{
		Detection: Detection{Capability: "gtk4-clipboard", Evidence: []string{"task Verification compiles clipboard code against GTK4"}},
		ReviewRules: []ReviewRule{
			{Capability: "gtk4-clipboard", ID: "access", Guidance: "GTK4 obtains the system clipboard with gdk_display_get_clipboard(gdk_display_get_default()); GTK3 gtk_clipboard_* calls and the invented gdk_clipboard_get API are not GTK4 interfaces."},
			{Capability: "gtk4-clipboard", ID: "publish-bytes", Guidance: "To publish serialized image/png bytes in GTK4, wrap the GBytes with gdk_content_provider_new_for_bytes(\"image/png\", bytes), pass that provider to gdk_clipboard_set_content, check its gboolean result, and balance the provider reference. gdk_clipboard_set_image, gdk_clipboard_set_request_callback, and gdk_content_provider_new_for_pixbuf are not GTK4 APIs."},
			{Capability: "gtk4-clipboard", ID: "completion", Guidance: "GTK4 exposes no request-paintable acknowledgement callback. gdk_clipboard_set_content returning true confirms publication; gdk_clipboard_store_async followed by gdk_clipboard_store_finish confirms a persistence request, not that a compositor consumed the image. Do not invent a callback or use gdk_clipboard_read_async as consumption acknowledgement. Expose the chosen completion to the caller through its GTask/callback, including a boolean/result/error that distinguishes publication success from failure; merely invoking a void callback on both paths makes Delivering -> Terminated observable but hides whether delivery succeeded."},
			{Capability: "gtk4-clipboard", ID: "main-context", Guidance: "PNG serialization may be expensive and belongs in a worker; marshal only GDK clipboard access and UI/state completion onto the main context. Putting serialization itself inside g_idle_add can block the GTK event loop."},
		},
	}
}

var (
	uncheckedClipboardContent = regexp.MustCompile(`(?m)^\s*gdk_clipboard_set_content\s*\(`)
	inventedClipboardReady    = regexp.MustCompile(`(?i)(?:notify::ready|g_signal_connect(?:_object)?\s*\([^;]*clipboard[^;]*,\s*"ready")`)
)

func (GTK4Clipboard) Inspect(ctx Context) ([]Inspection, error) {
	verification := strings.ToLower(ctx.TaskVerification)
	if !strings.Contains(verification, "gtk4") || !strings.Contains(verification, "clipboard") {
		return nil, nil
	}
	path := verificationSource(ctx.ProjectRoot, ctx.TaskVerification)
	if path == "" {
		return nil, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Inspection
	publishPolicy := ctx.Policies["gtk4-clipboard.publish-result"]
	if publishPolicy != "off" && uncheckedClipboardContent.Match(body) {
		enforcement := Required
		if publishPolicy == "diagnostic" {
			enforcement = Diagnostic
		}
		out = append(out, Inspection{
			Capability: "gtk4-clipboard", Name: "publish-result", Enforcement: enforcement,
			Detail: "gdk_clipboard_set_content returns gboolean but its result is ignored; branch on failure before reporting or scheduling successful delivery",
		})
	}
	completionPolicy := ctx.Policies["gtk4-clipboard.completion-signal"]
	if completionPolicy != "off" && inventedClipboardReady.Match(body) {
		enforcement := Required
		if completionPolicy == "diagnostic" {
			enforcement = Diagnostic
		}
		out = append(out, Inspection{
			Capability: "gtk4-clipboard", Name: "completion-signal", Enforcement: enforcement,
			Detail: "GdkClipboard has no ready/notify::ready acknowledgement signal; use checked set_content for publication or store_async/store_finish for persistence, then expose that chosen completion through the caller's GTask/callback",
		})
	}
	return out, nil
}

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

var lowBitCounter = regexp.MustCompile(`(?s)while\s*\(\s*mask\s*&\s*1\s*\).*?mask\s*>>=`)

// Inspect catches a recurrent semantic error that syntax checks cannot: a
// helper that counts consecutive low one-bits is called with an unshifted X11
// channel mask such as 0xff0000, producing a width of zero. This is deliberately
// narrow; unfamiliar conversion strategies remain the reviewer's domain.
func (X11Image) Inspect(ctx Context) ([]Inspection, error) {
	if !x11Verification(ctx.TaskVerification) {
		return nil, nil
	}
	policy := ctx.Policies["x11-image.channel-mask-flow"]
	if policy == "off" {
		return nil, nil
	}
	enforcement := Required
	if policy == "diagnostic" {
		enforcement = Diagnostic
	}
	path := verificationSource(ctx.ProjectRoot, ctx.TaskVerification)
	if path == "" {
		return nil, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	text := string(body)
	if !lowBitCounter.MatchString(text) {
		return nil, nil
	}
	for _, channel := range []string{"red", "green", "blue"} {
		if strings.Contains(text, "count_one_bits("+channel+"_mask)") &&
			strings.Contains(text, "count_trailing_zeroes("+channel+"_mask)") {
			return []Inspection{{
				Capability: "x11-image", Name: "channel-mask-flow", Enforcement: enforcement,
				Detail: fmt.Sprintf("%s channel width is counted from the unshifted mask; a low-bit counter returns zero for shifted fields such as 0xff0000. Count width from (%s_mask >> %s_shift) or from the mask after removing trailing zeroes", channel, channel, channel),
			}}, nil
		}
	}
	return nil, nil
}

func x11Verification(command string) bool {
	lower := strings.ToLower(command)
	return strings.Contains(lower, "x11") || strings.Contains(lower, "xfixes")
}

func verificationSource(root, command string) string {
	for _, field := range strings.Fields(command) {
		field = strings.Trim(field, "'\"")
		if strings.ToLower(filepath.Ext(field)) != ".c" {
			continue
		}
		clean := filepath.Clean(field)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			continue
		}
		return filepath.Join(root, clean)
	}
	return ""
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
			{Capability: "glib-async", ID: "allocation", Guidance: "g_new0(Type, 1) allocates exactly one zero-initialized Type; the second argument is the element count. Do not report it as allocating two objects or replace it merely with g_new."},
			{Capability: "glib-async", ID: "task-lifetime", Guidance: "g_task_run_in_thread manages its worker reference, but the initiating function must still g_object_unref its own GTask reference after scheduling. With a manual g_thread_new, pass g_object_ref(task) to the worker, g_object_unref it when the worker finishes, and immediately g_thread_unref the returned handle for detached execution; do not join from the caller's main context or treat a borrowed task pointer as owned."},
			{Capability: "glib-async", ID: "deferred-input-lifetime", Guidance: "A pointer retained for g_idle_add, an async callback, a worker, or a queue outlives the initiating call. Copy its data, take a reference, or make ownership transfer explicit; merely storing a borrowed input in a context struct can become a use-after-free."},
			{Capability: "glib-async", ID: "callback-context", Guidance: "If a public async API accepts callback user_data, its callback signature and every completion path must pass that exact user_data back. Storing it without delivering it is a broken API contract even when compilation succeeds."},
			{Capability: "glib-async", ID: "idle-source-id", Guidance: "g_idle_add returns a source ID guaranteed to be greater than zero. Do not invent a g_idle_add()==0 allocation-failure branch or run its main-context callback synchronously from a worker; use g_idle_add_full with a destroy notify when source-data cleanup needs an explicit ownership contract."},
			{Capability: "glib-async", ID: "g-task-api", Guidance: "For GTask, g_task_new takes (source_object, cancellable, GAsyncReadyCallback, callback_data). GAsyncReadyCallback is exactly void callback(GObject *source_object, GAsyncResult *result, gpointer user_data). GTaskThreadFunc is exactly void worker(GTask *task, gpointer source_object, gpointer task_data, GCancellable *cancellable). Return a pointer with g_task_return_pointer and receive it only with g_task_propagate_pointer(G_TASK(result), &error). There are no g_task_propose_pointer or g_task_propose_error APIs."},
			{Capability: "glib-async", ID: "completion", Guidance: "Every success and error path must complete the async result exactly once, without making the caller's main context wait for the worker. A worker completes its GTask with one g_task_return_* call; the resulting GAsyncReadyCallback consumes that completed result with the corresponding g_task_propagate_* call and must not call g_task_return_* on G_TASK(result) again."},
			{Capability: "glib-async", ID: "nested-destroy", Guidance: "A GTask result destroy-notify must release nested owned allocations as well as the outer result object."},
			{Capability: "glib-async", ID: "boxed-release", Guidance: "GBytes is a ref-counted boxed type, not a GObject. Release a GBytes* with g_bytes_unref (for example g_clear_pointer(&bytes, g_bytes_unref)), never g_clear_object or g_object_unref."},
			{Capability: "glib-async", ID: "context-initialization", Guidance: "Every field read by a deferred callback must be initialized before scheduling it. Passing ctx as GTask task_data does not initialize a separate ctx->task back-reference; either assign an explicitly owned reference or have the callback receive the task through its actual API contract."},
			{Capability: "glib-async", ID: "task-validation", Guidance: "g_task_is_valid(result, source_object) is valid when both the GTask and the check use a NULL source_object; NULL does not make the check always false. Report a mismatch only when the task was created with a different source object."},
		},
	}
}

var (
	threadWorkerArgument = regexp.MustCompile(`g_thread_new\s*\(\s*[^,]+,\s*([A-Za-z_][A-Za-z0-9_]*)\s*,`)
	ignoredThreadHandle  = regexp.MustCompile(`(?m)(?:^|[;{}])\s*g_thread_new\s*\([^;\n]*\)\s*;`)
	gbytesDeclaration    = regexp.MustCompile(`\bGBytes\s*\*\s*([A-Za-z_][A-Za-z0-9_]*)`)
	gTaskCreation        = regexp.MustCompile(`(?:\bGTask\s*\*\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*g_task_new\s*\(`)
	gTaskReadyCallback   = regexp.MustCompile(`g_task_new\s*\(\s*[^,]*,\s*[^,]*,\s*([A-Za-z_][A-Za-z0-9_]*)\s*,`)
	uninitializedCtxTask = regexp.MustCompile(`g_task_set_task_data\s*\(\s*([A-Za-z_][A-Za-z0-9_]*)\s*,\s*([A-Za-z_][A-Za-z0-9_]*)\s*,[^;]*\)`)
)

// InspectReviewFindings rejects remedies that contradict GIO's declared
// callback ABI. The check is deliberately about the proposed fix, not the
// reviewer's prose: a mistaken issue can still prescribe a valid remedy.
func (GLibAsync) InspectReviewFindings(findings []ReviewFinding) []ReviewFindingInspection {
	var out []ReviewFindingInspection
	for i, finding := range findings {
		fix := strings.ToLower(strings.TrimSpace(finding.Fix))
		mentionsReadyCallback := strings.Contains(fix, "gasyncreadycallback") ||
			(strings.Contains(fix, "gasyncresult") && strings.Contains(fix, "gpointer"))
		prescribesTwoArguments := strings.Contains(fix, "exactly two") ||
			strings.Contains(fix, "two parameter") || strings.Contains(fix, "two-parameter") ||
			strings.Contains(fix, "no source_object") || strings.Contains(fix, "remove source_object") ||
			(strings.Contains(fix, "signature") && strings.Contains(fix, "gasyncresult") &&
				strings.Contains(fix, "gpointer") && !strings.Contains(fix, "gobject"))
		if mentionsReadyCallback && prescribesTwoArguments {
			out = append(out, ReviewFindingInspection{Index: i, Inspection: Inspection{
				Capability: "glib-async", Name: "invalid-ready-callback-remedy", Enforcement: Required,
				Detail: fmt.Sprintf("finding %d is inadmissible: its fix prescribes a two-argument GAsyncReadyCallback, but GIO declares (GObject *source_object, GAsyncResult *result, gpointer user_data)", i),
			}})
		}
	}
	return out
}

// Inspect verifies the C-level return contract of functions actually passed
// to g_thread_new. GCC accepts a gpointer worker that falls off its success
// path even under -Wall/-Wextra/-Werror, while GLib's trampoline consumes the
// function's return value.
func (GLibAsync) Inspect(ctx Context) ([]Inspection, error) {
	verification := strings.ToLower(ctx.TaskVerification)
	if !strings.Contains(verification, "glib-2.0") && !strings.Contains(verification, "gio-2.0") && !strings.Contains(verification, "gtk") {
		return nil, nil
	}
	path := verificationSource(ctx.ProjectRoot, ctx.TaskVerification)
	if path == "" {
		return nil, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	text := string(body)
	var out []Inspection
	threadPolicy := ctx.Policies["glib-async.thread-return"]
	if threadPolicy != "off" {
		enforcement := policyEnforcement(threadPolicy)
		if ignoredThreadHandle.MatchString(text) {
			out = append(out, Inspection{
				Capability: "glib-async", Name: "thread-handle", Enforcement: enforcement,
				Detail: "g_thread_new returns an owned GThread handle but this call discards it; assign the result and unref it for detached execution (or join it when the design requires joining)",
			})
		} else {
			for _, match := range threadWorkerArgument.FindAllStringSubmatch(text, -1) {
				worker := match[1]
				functionBody, pointerReturn, found := cThreadFunction(text, worker)
				if !found {
					continue
				}
				if !pointerReturn {
					out = append(out, Inspection{
						Capability: "glib-async", Name: "thread-return", Enforcement: enforcement,
						Detail: fmt.Sprintf("g_thread_new worker %s is declared void; declare it with the GThreadFunc-compatible gpointer return type and return NULL (or another explicit gpointer)", worker),
					})
					break
				}
				if !workerReturnsValue(functionBody) {
					out = append(out, Inspection{
						Capability: "glib-async", Name: "thread-return", Enforcement: enforcement,
						Detail: fmt.Sprintf("g_thread_new worker %s can reach the closing brace without returning a gpointer; end its fallthrough path with return NULL (or another explicit gpointer)", worker),
					})
					break
				}
			}
		}
	}

	boxedPolicy := ctx.Policies["glib-async.boxed-release"]
	if boxedPolicy != "off" {
		for _, match := range gbytesDeclaration.FindAllStringSubmatch(text, -1) {
			name := match[1]
			member := `(?:[A-Za-z_][A-Za-z0-9_]*\s*->\s*)?` + regexp.QuoteMeta(name)
			wrongClear := regexp.MustCompile(`g_clear_object\s*\(\s*&\s*` + member + `\s*\)`)
			wrongUnref := regexp.MustCompile(`g_object_unref\s*\(\s*` + member + `\s*\)`)
			if wrongClear.MatchString(text) || wrongUnref.MatchString(text) {
				out = append(out, Inspection{
					Capability: "glib-async", Name: "boxed-release", Enforcement: policyEnforcement(boxedPolicy),
					Detail: fmt.Sprintf("%s is declared GBytes* but is released with GObject semantics; use g_bytes_unref (or g_clear_pointer(&%s, g_bytes_unref))", name, name),
				})
				break
			}
		}
	}

	contextPolicy := ctx.Policies["glib-async.context-initialization"]
	if contextPolicy != "off" {
		for _, match := range uninitializedCtxTask.FindAllStringSubmatch(text, -1) {
			task, contextName := match[1], match[2]
			usesBackref := regexp.MustCompile(`g_task_return_[A-Za-z0-9_]+\s*\(\s*` + regexp.QuoteMeta(contextName) + `\s*->\s*task\b`)
			assignsBackref := regexp.MustCompile(`\b` + regexp.QuoteMeta(contextName) + `\s*->\s*task\s*=`)
			if usesBackref.MatchString(text) && !assignsBackref.MatchString(text) {
				out = append(out, Inspection{
					Capability: "glib-async", Name: "context-initialization", Enforcement: policyEnforcement(contextPolicy),
					Detail: fmt.Sprintf("%s is passed as task_data for %s, but a deferred callback completes %s->task and that field is never assigned", contextName, task, contextName),
				})
				break
			}
		}
	}

	ownerPolicy := ctx.Policies["glib-async.task-owner"]
	if ownerPolicy != "off" {
		for _, match := range gTaskCreation.FindAllStringSubmatch(text, -1) {
			task := match[1]
			runs := regexp.MustCompile(`g_task_run_in_thread\s*\(\s*` + regexp.QuoteMeta(task) + `\s*,`)
			releases := regexp.MustCompile(`(?:g_object_unref\s*\(\s*` + regexp.QuoteMeta(task) + `\s*\)|g_clear_object\s*\(\s*&\s*` + regexp.QuoteMeta(task) + `\s*\))`)
			if runs.MatchString(text) && !releases.MatchString(text) {
				out = append(out, Inspection{
					Capability: "glib-async", Name: "task-owner", Enforcement: policyEnforcement(ownerPolicy),
					Detail: fmt.Sprintf("%s is created with g_task_new and scheduled with g_task_run_in_thread, but the initiating reference is never released with g_object_unref", task),
				})
				break
			}
		}
	}

	completionPolicy := ctx.Policies["glib-async.ready-callback-completion"]
	if completionPolicy != "off" {
		for _, match := range gTaskReadyCallback.FindAllStringSubmatch(text, -1) {
			callback := match[1]
			functionBody, found := cNamedFunctionBody(text, callback)
			if !found || !readyCallbackRecompletesResult(functionBody) {
				continue
			}
			out = append(out, Inspection{
				Capability: "glib-async", Name: "ready-callback-completion", Enforcement: policyEnforcement(completionPolicy),
				Detail: fmt.Sprintf("GAsyncReadyCallback %s calls g_task_return_* on the GTask obtained from its result; the worker has already completed that result, so consume it with g_task_propagate_* instead of completing it again", callback),
			})
			break
		}
	}
	return out, nil
}

func policyEnforcement(policy string) Enforcement {
	if policy == "diagnostic" {
		return Diagnostic
	}
	return Required
}

func cThreadFunction(source, name string) (body string, pointerReturn bool, found bool) {
	definition := regexp.MustCompile(`(?m)(?:^|\n)\s*(?:static\s+)?(gpointer\s+|void\s*\*\s*|void\s+)` + regexp.QuoteMeta(name) + `\s*\([^)]*\)\s*\{`)
	location := definition.FindStringSubmatchIndex(source)
	if location == nil {
		return "", false, false
	}
	returnType := strings.TrimSpace(source[location[2]:location[3]])
	open := location[1] - 1
	depth := 0
	for i := open; i < len(source); i++ {
		switch source[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return source[open+1 : i], returnType != "void", true
			}
		}
	}
	return "", returnType != "void", true
}

func cNamedFunctionBody(source, name string) (string, bool) {
	definition := regexp.MustCompile(`(?m)(?:^|\n)\s*(?:static\s+)?[A-Za-z_][A-Za-z0-9_\s*]*\s+` + regexp.QuoteMeta(name) + `\s*\([^;]*?\)\s*\{`)
	location := definition.FindStringIndex(source)
	if location == nil {
		return "", false
	}
	openOffset := strings.LastIndex(source[location[0]:location[1]], "{")
	if openOffset < 0 {
		return "", false
	}
	open := location[0] + openOffset
	depth := 0
	for i := open; i < len(source); i++ {
		switch source[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return source[open+1 : i], true
			}
		}
	}
	return "", false
}

func readyCallbackRecompletesResult(body string) bool {
	direct := regexp.MustCompile(`g_task_return_[A-Za-z0-9_]+\s*\(\s*G_TASK\s*\(`)
	if direct.MatchString(body) {
		return true
	}
	for _, match := range regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*=\s*G_TASK\s*\(`).FindAllStringSubmatch(body, -1) {
		returnsAlias := regexp.MustCompile(`g_task_return_[A-Za-z0-9_]+\s*\(\s*` + regexp.QuoteMeta(match[1]) + `\b`)
		if returnsAlias.MatchString(body) {
			return true
		}
	}
	return false
}

func workerReturnsValue(body string) bool {
	trimmed := strings.TrimSpace(body)
	return regexp.MustCompile(`(?s)(?:return\s+[^;]+;|g_thread_exit\s*\([^;]*\)\s*;)\s*$`).MatchString(trimmed)
}
