package buildinfo

var Version = "0.1.0-m1"

// DesktopQEMUMarker is compiled into every PVM Desktop that can set up QEMU
// builds (v1.4.0 and later): pvm looks for it in pvm-desktop.exe, whose
// release build carries no readable version.
const DesktopQEMUMarker = "pvm-desktop:qemu-builds/1"
