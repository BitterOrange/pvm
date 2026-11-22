# pvm starts the Xpra server through this file, which changes three
# things in it before it runs.
#
# Text a host input method commits, such as Chinese from Microsoft Pinyin,
# arrives as a key press with keycode 0, the character as its string and
# a name such as U+FF08. The server only presses keycodes its keymap has,
# so it dropped most of them, and read some names as the X keysym of the
# same number: U+FF08, a full-width parenthesis, pressed BackSpace.
# Xpra runs ibus-daemon for the display and the apps use it, so pvm adds
# an ibus engine that lets every key through and commits such text into
# the focused app as a whole: no keycode is borrowed, and the modifiers
# still held, such as Shift for a full-width parenthesis, do not touch it.
# An app without ibus gets the text typed with xdotool, which needs one
# empty keycode to borrow for each character; 254 (XF86WWAN) is cleared
# for it, as the full keymap leaves none.
#
# The engine also hears where the focused app's text cursor is, and sends
# it to the client as the window metadata "pvm-caret": the client pvm
# ships places the host input method's window there. Other clients keep
# the key unused. Chromium, and so Electron apps such as Claude Desktop,
# tells ibus where its cursor is only while it handles a key, and the keys
# of a host input method never reach it: after a left click, a text field
# taking the focus and committed text, the server presses a key no
# keyboard has, which the engine eats, so that the app tells it.
#
# A maximum window size larger than any screen is dropped: the Windows
# client applied it and removed the window's maximize button, and GTK
# gives Firefox 16366x16337.
import queue
import re
import subprocess
import sys
import threading
import time
from time import monotonic

from xpra.platform import init, set_default_name

set_default_name("Xpra")
init()

from xpra.os_util import gi_import  # noqa: E402
from xpra.x11.models import size_hints_util  # noqa: E402

GLib = gi_import("GLib")

# both sides at least this large is no limit
_NO_LIMIT = 8192
_sanitize_size_hints = size_hints_util.sanitize_size_hints


def sanitize_size_hints(size_hints):
    _sanitize_size_hints(size_hints)
    maxs = size_hints.get("maximum-size") if size_hints else None
    if maxs and maxs[0] >= _NO_LIMIT and maxs[1] >= _NO_LIMIT:
        del size_hints["maximum-size"]


# before the window model imports it by name
size_hints_util.sanitize_size_hints = sanitize_size_hints

from xpra.server.subsystem.keyboard import KeyboardServer  # noqa: E402

_text = queue.Queue()


def _typist():
    while True:
        s = _text.get()
        # an input method commits a word as one key event per character,
        # milliseconds apart: type it in one call
        while True:
            try:
                s += _text.get(timeout=0.03)
            except queue.Empty:
                break
        try:
            keymap = subprocess.run(["xmodmap", "-pke"], capture_output=True, text=True, timeout=10).stdout
            if not re.search(r"^keycode 254 =\s*$", keymap, re.M):
                subprocess.run(["xmodmap", "-e", "keycode 254 ="], timeout=10, check=False)
                # applications reload the changed keymap; typing at once
                # lost the first character
                time.sleep(0.2)
            # applications look the borrowed keycode up after the event; a
            # shorter delay lost characters
            subprocess.run(["xdotool", "type", "--clearmodifiers", "--delay", "40", "--", s], timeout=60, check=False)
            time.sleep(0.05)
        except Exception as e:  # keep typing later text
            print("pvm: xdotool type failed: %s" % e, file=sys.stderr)


threading.Thread(target=_typist, daemon=True).start()

# the server, once known, and the engine of the focused input context
_server = None
_focused = None
_caret = None
_caret_sent = {}
IBus = None
# keys no keyboard has, the first the keymap holds is pressed
_NUDGE_KEYS = ("XF86Launch9", "XF86Launch8", "F15", "F14", "F13")
# the app takes a frame or more to move its cursor, longer when busy or
# when it switches views
_NUDGE_DELAYS = (50, 300, 1000)
# the keycode pressed and until when the engine eats it
_nudge = None
_nudge_gen = 0


def _send_caret():
    """Sends where the focused app's text cursor is, relative to its
    window, once per change."""
    srv = _server
    if srv is None or _caret is None:
        return
    wid = getattr(srv, "_has_focus", 0)
    window = srv._id_to_window.get(wid) if wid else None
    if window is None:
        return
    try:
        gx, gy = window.get_property("geometry")[:2]
    except Exception:
        return
    x, y, w, h = _caret
    caret = (x - gx, y - gy, w, h)
    if _caret_sent.get(wid) == caret:
        return
    _caret_sent[wid] = caret
    for ss in list(srv._server_sources.values()):
        try:
            ss.send("window-metadata", wid, {"pvm-caret": caret})
        except Exception as e:
            print("pvm: send the text cursor: %s" % e, file=sys.stderr)


def _schedule_nudge():
    """Has the focused app tell where its text cursor is, once its cursor
    has moved; a later call replaces the earlier presses."""
    global _nudge_gen
    _nudge_gen += 1
    for ms in _NUDGE_DELAYS:
        GLib.timeout_add(ms, _press_nudge, _nudge_gen)


def _press_nudge(gen):
    global _nudge
    srv = _server
    if gen != _nudge_gen or srv is None or _focused is None:
        return False
    from xpra.x11.bindings.keyboard import X11KeyboardBindings
    from xpra.x11.error import xlog
    keycode = 0
    with xlog:
        keyboard = X11KeyboardBindings()
        for name in _NUDGE_KEYS:
            keycodes = keyboard.get_keycodes(name)
            if keycodes:
                keycode = keycodes[0]
                break
    if keycode:
        _nudge = (keycode, monotonic() + 0.5)
        srv.fake_key(keycode, True)
        srv.fake_key(keycode, False)
    return False


def _commit(s):
    """Commits s into the focused app through ibus; False when no app
    with ibus has the focus."""
    if _focused is None or IBus is None:
        return False
    try:
        _focused.commit_text(IBus.Text.new_from_string(s))
        return True
    except Exception as e:
        print("pvm: ibus commit failed: %s" % e, file=sys.stderr)
        return False


def _start_engine(tries=[0]):
    """Registers the engine once ibus-daemon, which Xpra starts after the
    display, takes connections; again after it restarts."""
    global IBus
    try:
        IBus = gi_import("IBus")
        GObject = gi_import("GObject")
    except Exception as e:
        print("pvm: no ibus bindings; input method text is typed with xdotool: %s" % e, file=sys.stderr)
        return False
    bus = IBus.Bus()
    if not bus.is_connected():
        tries[0] += 1
        if tries[0] > 120:
            print("pvm: ibus-daemon did not come up; input method text is typed with xdotool", file=sys.stderr)
            return False
        return True  # the timer tries again

    class PvmEngine(IBus.Engine):
        __gtype_name__ = "PvmEngine"

        def do_process_key_event(self, keyval, keycode, state):
            # ibus counts keycodes from 8 lower than X
            return bool(_nudge) and keycode + 8 == _nudge[0] and monotonic() < _nudge[1]

        def do_focus_in(self):
            global _focused, _caret
            _focused = self
            # the cursor it reported while another context had the focus
            if getattr(self, "caret", None):
                _caret = self.caret
                _send_caret()
            # a text field an app focused itself, such as after switching
            # views, is somewhere else; not for a focus the press gave
            if not (_nudge and monotonic() < _nudge[1]):
                _schedule_nudge()

        def do_focus_out(self):
            global _focused
            if _focused is self:
                _focused = None

        def do_set_cursor_location(self, x, y, w, h):
            global _caret
            if w < 0 or h <= 0:
                return
            self.caret = (x, y, w, h)
            if _focused is self:
                _caret = self.caret
                _send_caret()

    try:
        factory = IBus.Factory.new(bus.get_connection())
        factory.add_engine("pvm", GObject.type_from_name("PvmEngine"))
        component = IBus.Component(name="org.pvm.Engine", description="pvm", version="1", license="GPL",
                                   author="pvm", homepage="", command_line="", textdomain="")
        # layout "default" keeps the keymap the client set
        component.add_engine(IBus.EngineDesc(name="pvm", longname="pvm", description="host input method text",
                                             language="other", license="GPL", author="pvm", icon="", layout="default"))
        bus.register_component(component)

        def engine_set(b, res):
            try:
                b.set_global_engine_async_finish(res)
            except Exception as e:
                print("pvm: ibus engine failed; input method text is typed with xdotool: %s" % e, file=sys.stderr)

        # ibus-daemon asks this process for the engine before it answers,
        # so a blocking call would wait for itself
        bus.set_global_engine_async("pvm", -1, None, engine_set)
    except Exception as e:
        print("pvm: ibus engine failed; input method text is typed with xdotool: %s" % e, file=sys.stderr)
        return False

    def lost(_bus):
        global _focused
        _focused = None
        tries[0] = 0
        GLib.timeout_add(1000, _start_engine)

    bus.connect("disconnected", lost)
    # the objects live as long as the connection
    _start_engine.held = (bus, factory, component, PvmEngine)
    print("pvm: ibus engine ready", file=sys.stderr)
    return False


GLib.timeout_add(1000, _start_engine)

_init = KeyboardServer.__init__


def __init__(self, *args, **kwargs):
    global _server
    _server = self
    _init(self, *args, **kwargs)


KeyboardServer.__init__ = __init__

from xpra.server.subsystem.pointer import PointerServer  # noqa: E402

_button_action = PointerServer.do_process_button_action


def do_process_button_action(self, proto, device_id, wid, button, pressed, pointer, props):
    r = _button_action(self, proto, device_id, wid, button, pressed, pointer, props)
    # a click into text moves the cursor
    if button == 1 and not pressed:
        _schedule_nudge()
    return r


PointerServer.do_process_button_action = do_process_button_action
_get_keycode = KeyboardServer.get_keycode


def get_keycode(self, ss, client_keycode, keyname, pressed, modifiers, keyval, keystr, group):
    global _server
    _server = self
    text = client_keycode == 0 and bool(keystr) and keystr.isprintable()
    if text and keyname.startswith("U+"):
        keycode, rgroup = -1, group
    else:
        keycode, rgroup = _get_keycode(self, ss, client_keycode, keyname, pressed, modifiers, keyval, keystr, group)
    if keycode < 0 and pressed and text:
        if _commit(keystr):
            _schedule_nudge()
        else:
            _text.put(keystr)
    return keycode, rgroup


KeyboardServer.get_keycode = get_keycode

from xpra.scripts.main import main  # noqa: E402

sys.exit(main("xpra", ["xpra"] + sys.argv[1:]))
