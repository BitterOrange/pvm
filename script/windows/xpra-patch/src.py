# pvm adds this file next to the client's xpra/audio/src.pyc; Python loads
# a source file before compiled code of the same name. It runs the
# original module, then changes one thing in it.
#
# The microphone goes to the VM in 60 ms Opus frames instead of 20 ms. In
# the VM every packet wakes several processes on other vCPUs, and under
# WHPX each wakeup exits to QEMU: a third of the packets, for 40 ms more
# delay. pvm's Xpra server does the same for the sound the VM plays.
import marshal
import os

with open(os.path.join(os.path.dirname(__file__), "src.pyc"), "rb") as _f:
    exec(marshal.loads(_f.read()[16:]), globals())

from xpra.audio import gstreamer_util as _gstreamer_util  # noqa: E402

_gstreamer_util.ENCODER_DEFAULT_OPTIONS.setdefault("opusenc", {})["frame-size"] = 60
