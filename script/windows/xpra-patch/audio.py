# pvm adds this file next to the client's xpra/client/subsystem/audio.pyc;
# Python loads a source file before compiled code of the same name. It runs
# the original module, then adds one thing to it.
#
# The microphone records from the Windows default input device as it was
# when forwarding started. Plugging in or removing a headset changes the
# default, but the client kept recording the old device, or nothing once
# that device was gone, until it was attached again: it only follows the
# output device. While the microphone is on, the client now watches for
# device changes and restarts the microphone when the default input device
# is another one or its capture has stopped.
import marshal
import os
import sys

with open(os.path.join(os.path.dirname(__file__), "audio.pyc"), "rb") as _f:
    exec(marshal.loads(_f.read()[16:]), globals())

if sys.platform == "win32":
    import ctypes

    from xpra.os_util import gi_import

    _GLib = gi_import("GLib")
    # one plug or unplug sends several notifications
    _PVM_RESTART_DELAY_MS = 1000

    def _pvm_vcall(obj, index, *argtypes):
        vtbl = ctypes.cast(obj, ctypes.POINTER(ctypes.POINTER(ctypes.c_void_p)))[0]
        return ctypes.WINFUNCTYPE(ctypes.c_long, ctypes.c_void_p, *argtypes)(vtbl[index])

    def _pvm_default_input():
        """The id of the default input device, "" when there is none, None
        when it cannot be read."""
        from xpra.platform.win32 import audio_device_monitor as m
        ole32 = ctypes.windll.ole32
        # the thread may already be in another apartment, which is fine
        ole32.CoInitializeEx(None, 0)
        enum = ctypes.c_void_p()
        if ole32.CoCreateInstance(ctypes.byref(m.CLSID_MMDeviceEnumerator), None, m.CLSCTX_ALL,
                                  ctypes.byref(m.IID_IMMDeviceEnumerator), ctypes.byref(enum)) != 0:
            return None
        try:
            dev = ctypes.c_void_p()
            # IMMDeviceEnumerator::GetDefaultAudioEndpoint(eCapture, eConsole)
            get_default = _pvm_vcall(enum, 4, ctypes.c_int, ctypes.c_int, ctypes.POINTER(ctypes.c_void_p))
            if get_default(enum, 1, 0, ctypes.byref(dev)) != 0:
                return ""
            try:
                sid = ctypes.c_void_p()
                # IMMDevice::GetId
                if _pvm_vcall(dev, 5, ctypes.POINTER(ctypes.c_void_p))(dev, ctypes.byref(sid)) != 0:
                    return None
                try:
                    return ctypes.wstring_at(sid.value)
                finally:
                    ole32.CoTaskMemFree(sid)
            finally:
                _pvm_vcall(dev, 2)(dev)
        finally:
            _pvm_vcall(enum, 2)(enum)

    _start_sending_audio = AudioClient.start_sending_audio  # noqa: F821
    _stop_sending_audio = AudioClient.stop_sending_audio  # noqa: F821
    _cleanup = AudioClient.cleanup  # noqa: F821

    def _pvm_start_sending_audio(self, *args, **kwargs):
        # wanted even if no input device is there yet
        self._pvm_mic = (args, kwargs)
        try:
            _start_sending_audio(self, *args, **kwargs)
        except Exception:
            self._pvm_mic = None
            raise
        self._pvm_mic_input = _pvm_default_input()
        if not getattr(self, "_pvm_watching", False):
            from xpra.audio.device_monitor import start_device_monitor
            start_device_monitor(lambda: _pvm_device_changed(self))
            self._pvm_watching = True

    def _pvm_stop_sending_audio(self):
        self._pvm_mic = None
        _stop_sending_audio(self)

    def _pvm_device_changed(self):
        if getattr(self, "_pvm_mic", None) and not getattr(self, "_pvm_restart", 0):
            self._pvm_restart = _GLib.timeout_add(_PVM_RESTART_DELAY_MS, _pvm_restart_mic, self)

    def _pvm_restart_mic(self):
        self._pvm_restart = 0
        wanted = getattr(self, "_pvm_mic", None)
        if not wanted:
            return False
        current = _pvm_default_input()
        ss = self.audio_source
        alive = ss is not None and ss.get_state() == "active"
        if alive and current == getattr(self, "_pvm_mic_input", None):
            return False
        log.info("default input device changed, restarting microphone")  # noqa: F821
        _stop_sending_audio(self)
        try:
            _start_sending_audio(self, *wanted[0], **wanted[1])
        except Exception as e:
            log.warn("Warning: cannot restart the microphone: %s", e)  # noqa: F821
        self._pvm_mic_input = current
        return False

    def _pvm_cleanup(self):
        if getattr(self, "_pvm_restart", 0):
            _GLib.source_remove(self._pvm_restart)
            self._pvm_restart = 0
        if getattr(self, "_pvm_watching", False):
            from xpra.audio.device_monitor import stop_device_monitor
            stop_device_monitor()
            self._pvm_watching = False
        _cleanup(self)

    AudioClient.start_sending_audio = _pvm_start_sending_audio  # noqa: F821
    AudioClient.stop_sending_audio = _pvm_stop_sending_audio  # noqa: F821
    AudioClient.cleanup = _pvm_cleanup  # noqa: F821
