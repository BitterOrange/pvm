# pvm runs this as the VM user each time the app display starts, with
# the VM's keyring setting as its argument and, for host and auto, the
# keyring password pvm keeps on the host on stdin.
#
# Apps there, such as Claude Desktop, keep secrets in the Secret Service,
# which gnome-keyring locks until a desktop login unlocks it; the app
# display has no login. Chromium encrypts with a key from the keyring
# while it can reach it, and with a fixed key while it cannot, and cannot
# read what it stored the other way: a start with the keyring locked
# ended the app's sign-in.
#
# host: the "login" keyring is encrypted with the password from the host
# and unlocked here, before any app starts. One that is missing is
# created; pvm's earlier one, stored unencrypted with an empty password,
# gets the password, which keeps its contents. A keyring with another
# password is left alone, and this exits 3.
# auto: the VM has no setting yet. pvm's earlier keyring moves to host as
# above, and this prints PVM_KEYRING=host; with none, it prints
# PVM_KEYRING=off and touches nothing.
import os
import sys

d = os.path.expanduser("~/.local/share/keyrings")
path = os.path.join(d, "login.keyring")


def plain():
    """Whether the login keyring is stored unencrypted: an empty password."""
    try:
        with open(path, "rb") as f:
            return f.read(9) == b"[keyring]"
    except FileNotFoundError:
        return False


mode = sys.argv[1]
if mode == "auto":
    if not plain():
        print("PVM_KEYRING=off")
        sys.exit(0)
elif mode != "host":
    sys.exit("pvm: unknown keyring mode %s" % mode)

password = sys.stdin.read().strip().encode()
if len(password) != 64:
    sys.exit("pvm: no keyring password on stdin")

from gi.repository import Gio, GLib  # noqa: E402

SECRETS = "org.freedesktop.secrets"
SERVICE = "/org/freedesktop/secrets"
LOGIN = SERVICE + "/collection/login"
INTERNAL = "org.gnome.keyring.InternalUnsupportedGuiltRiddenInterface"

if not os.path.exists(path):
    # an empty-password keyring as a passwordless login makes one; it gets
    # the password below
    os.makedirs(d, mode=0o700, exist_ok=True)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w") as f:
        f.write("[keyring]\ndisplay-name=Login\nctime=0\nmtime=0\nlock-on-idle=false\nlock-after=false\n")
    default = os.path.join(d, "default")
    if not os.path.exists(default):
        with open(default, "w") as f:
            f.write("login")

bus = Gio.bus_get_sync(Gio.BusType.SESSION)


def call(obj, iface, method, args, rtype):
    return bus.call_sync(SECRETS, obj, iface, method, args, GLib.VariantType(rtype), 0, 10000, None).unpack()


_, session = call(SERVICE, "org.freedesktop.Secret.Service", "OpenSession",
                  GLib.Variant("(sv)", ("plain", GLib.Variant("s", ""))), "(vo)")


def secret(value):
    return GLib.Variant("(oayays)", (session, b"", value, "text/plain"))


def locked():
    return call(LOGIN, "org.freedesktop.DBus.Properties", "Get",
                GLib.Variant("(ss)", ("org.freedesktop.Secret.Collection", "Locked")), "(v)")[0]


if plain():
    call(SERVICE, INTERNAL, "UnlockWithMasterPassword",
         GLib.Variant.new_tuple(GLib.Variant("o", LOGIN), secret(b"")), "()")
    call(SERVICE, INTERNAL, "ChangeWithMasterPassword",
         GLib.Variant.new_tuple(GLib.Variant("o", LOGIN), secret(b""), secret(password)), "()")
    if plain():
        sys.exit("pvm: the keyring kept no password")
try:
    call(SERVICE, INTERNAL, "UnlockWithMasterPassword",
         GLib.Variant.new_tuple(GLib.Variant("o", LOGIN), secret(password)), "()")
except GLib.Error:
    if locked():
        sys.exit(3)
    raise
if locked():
    sys.exit(3)
if mode == "auto":
    print("PVM_KEYRING=host")
