# pvm adds this file next to the client's xpra/client/gui/window_base.pyc;
# Python loads a source file before compiled code of the same name. It runs
# the original module, then adds one thing to it.
#
# The host input method's windows, such as Microsoft Pinyin's candidates,
# opened at the top left of an app's window: the client does not know
# where the app's text cursor is. pvm's Xpra server sends it as the window
# metadata "pvm-caret", (x, y, width, height) in the window, and the
# window places the input method's composition and candidate windows
# there, as a Windows program does at its own caret.
import marshal
import os
import sys

with open(os.path.join(os.path.dirname(__file__), "window_base.pyc"), "rb") as _f:
    exec(marshal.loads(_f.read()[16:]), globals())

if sys.platform == "win32":
    import ctypes
    from ctypes import wintypes

    class _COMPOSITIONFORM(ctypes.Structure):
        _fields_ = [("dwStyle", wintypes.DWORD), ("ptCurrentPos", wintypes.POINT), ("rcArea", wintypes.RECT)]

    class _CANDIDATEFORM(ctypes.Structure):
        _fields_ = [("dwIndex", wintypes.DWORD), ("dwStyle", wintypes.DWORD),
                    ("ptCurrentPos", wintypes.POINT), ("rcArea", wintypes.RECT)]

    _CFS_POINT = 0x0002
    _CFS_EXCLUDE = 0x0080
    _imm32 = ctypes.WinDLL("imm32")
    _imm32.ImmGetContext.argtypes = [wintypes.HWND]
    _imm32.ImmGetContext.restype = wintypes.HANDLE
    _imm32.ImmReleaseContext.argtypes = [wintypes.HWND, wintypes.HANDLE]
    _imm32.ImmSetCompositionWindow.argtypes = [wintypes.HANDLE, ctypes.POINTER(_COMPOSITIONFORM)]
    _imm32.ImmSetCandidateWindow.argtypes = [wintypes.HANDLE, ctypes.POINTER(_CANDIDATEFORM)]
    _user32 = ctypes.WinDLL("user32")
    _user32.CreateCaret.argtypes = [wintypes.HWND, wintypes.HANDLE, ctypes.c_int, ctypes.c_int]
    _user32.SetCaretPos.argtypes = [ctypes.c_int, ctypes.c_int]

    def _pvm_place_ime(window, caret):
        from xpra.platform.win32.gui import get_window_handle
        hwnd = get_window_handle(window)
        if not hwnd:
            return
        # server pixels to the window's, then to the device pixels Windows
        # counts in
        x, y, w, h = window.srect(*(int(v) for v in caret))
        scale = window.get_scale_factor() if hasattr(window, "get_scale_factor") else 1
        x, y, w, h = x * scale, y * scale, max(1, w * scale), max(1, h * scale)
        himc = _imm32.ImmGetContext(hwnd)
        if himc:
            try:
                comp = _COMPOSITIONFORM(_CFS_POINT, wintypes.POINT(x, y), wintypes.RECT(0, 0, 0, 0))
                _imm32.ImmSetCompositionWindow(himc, ctypes.byref(comp))
                # candidates go below the cursor and never cover its line
                for i in range(4):
                    cand = _CANDIDATEFORM(i, _CFS_EXCLUDE, wintypes.POINT(x, y + h), wintypes.RECT(x, y, x + w, y + h))
                    _imm32.ImmSetCandidateWindow(himc, ctypes.byref(cand))
            finally:
                _imm32.ImmReleaseContext(hwnd, himc)
        # input methods that follow the system caret instead; it is never
        # shown
        if _user32.CreateCaret(hwnd, None, w, h):
            _user32.SetCaretPos(x, y)

    _update_metadata = ClientWindowBase.update_metadata  # noqa: F821

    def _pvm_update_metadata(self, metadata):
        _update_metadata(self, metadata)
        caret = metadata.get("pvm-caret")
        if caret:
            try:
                _pvm_place_ime(self, caret)
            except Exception as e:
                print("pvm: place the input method window: %s" % e, file=sys.stderr)

    ClientWindowBase.update_metadata = _pvm_update_metadata  # noqa: F821
