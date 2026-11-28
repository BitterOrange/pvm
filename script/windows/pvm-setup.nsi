; pvm-setup.nsi: per-user Windows installer for pvm, PVM Desktop and the Xpra
; client for `pvm app`. QEMU is set up afterwards with `pvm qemu`. Built by
; script/windows/pack-setup.sh; do not run makensis directly.
;
; Required defines: VERSION, PVM_EXE, DESKTOP_EXE, XPRA_DIR, OUTFILE.

Unicode true
SetCompressor /SOLID lzma
RequestExecutionLevel user
ManifestDPIAware true

!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "WinMessages.nsh"

!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\pvm"
!define DESKTOP_LNK "$SMPROGRAMS\PVM Desktop.lnk"

Name "pvm ${VERSION}"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\pvm"
ShowInstDetails show
ShowUninstDetails show

!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_TEXT "pvm ${VERSION} is installed.$\r$\n$\r$\nOpen PVM Desktop from the Start menu: it sets up QEMU, which pvm needs to run VMs. Or open a new terminal and run:$\r$\n  pvm qemu install$\r$\n  pvm create --image ubuntu:26.04 --name dev1 --user dev"
; Unchecked: opening the app starts the pvm daemon, which setup must not do
; unasked.
!define MUI_FINISHPAGE_RUN "$INSTDIR\pvm-desktop.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Open PVM Desktop"
!define MUI_FINISHPAGE_RUN_NOTCHECKED
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!define MUI_UNCONFIRMPAGE_TEXT_TOP "pvm, PVM Desktop and the Xpra client will be removed. Your VMs and the QEMU builds pvm installed stay under ~/.pvm; the builds are in ~/.pvm\runtime\qemu\builds, which you can delete afterwards if you no longer need them."
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Var PvmHome

; The MSI installs into the same folder; mixing the two leaves two entries
; in Apps, and removing either one breaks the other.
Function .onInit
  ReadRegStr $0 HKCU "Software\pvm\msi" "PvmHome"
  ${If} $0 != ""
    MessageBox MB_ICONSTOP "pvm was installed with the .msi installer. Uninstall it in Settings > Apps first (your VMs are kept), then run this setup again, or update with the .msi." /SD IDOK
    SetErrorLevel 1
    Abort
  ${EndIf}
FunctionEnd

; Files under a running daemon, VM or PVM Desktop cannot be replaced.
; pvm-running.ps1 finds them by path; interactively it offers to close the app
; and stop the daemon (which stops running VMs) and waits for them to exit,
; silent runs refuse instead.
!macro EnsureStopped un
Function ${un}EnsureStopped
  InitPluginsDir
  File "/oname=$PLUGINSDIR\pvm-running.ps1" "${__FILEDIR__}\pvm-running.ps1"
  StrCpy $3 "prompt"
  ${If} ${Silent}
    StrCpy $3 "check"
  ${EndIf}
  DetailPrint "Checking for a running pvm..."
  nsExec::ExecToLog 'powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$PLUGINSDIR\pvm-running.ps1" -InstallDir "$INSTDIR" -PvmHome "$PvmHome" -Mode $3'
  Pop $1
  ${If} $1 != 0
    Abort "pvm, its VMs or PVM Desktop are still running; close PVM Desktop, run 'pvm daemon stop' and start setup again"
  ${EndIf}
FunctionEnd
!macroend
!insertmacro EnsureStopped ""
!insertmacro EnsureStopped "un."

; Defender scans freshly written files and briefly holds them open, so a
; rename right after staging can fail with a sharing violation. Retry for up
; to 10s; the error flag is set only if the last attempt failed.
!macro RenameRetry src dst
  StrCpy $R9 0
  ${Do}
    ClearErrors
    Rename "${src}" "${dst}"
    ${IfNot} ${Errors}
      ${Break}
    ${EndIf}
    IntOp $R9 $R9 + 1
    ${If} $R9 >= 100
      ${Break}
    ${EndIf}
    Sleep 100
  ${Loop}
  ClearErrors
  ${If} $R9 >= 100
    SetErrors
  ${EndIf}
!macroend

!macro UserPath action dir
  InitPluginsDir
  File "/oname=$PLUGINSDIR\user-path.ps1" "${__FILEDIR__}\user-path.ps1"
  nsExec::ExecToLog 'powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$PLUGINSDIR\user-path.ps1" -Action ${action} -Dir "${dir}"'
  Pop $1
  ${If} $1 != 0
    Abort "updating the user PATH failed (exit $1)"
  ${EndIf}
  SendMessage ${HWND_BROADCAST} ${WM_SETTINGCHANGE} 0 "STR:Environment" /TIMEOUT=5000
!macroend

Section "pvm"
  ReadEnvStr $PvmHome PVM_HOME
  ${If} $PvmHome == ""
    StrCpy $PvmHome "$PROFILE\.pvm"
  ${EndIf}

  Call EnsureStopped

  ; Stage every component and verify it before replacing anything.
  DetailPrint "Staging pvm ${VERSION}..."
  SetOutPath "$INSTDIR"
  File "/oname=pvm.new.exe" "${PVM_EXE}"
  nsExec::ExecToStack /OEM '"$INSTDIR\pvm.new.exe" version'
  Pop $1
  Pop $2
  StrLen $3 "${VERSION}"
  StrCpy $2 $2 $3
  ${If} $1 != 0
  ${OrIf} $2 != "${VERSION}"
    Delete "$INSTDIR\pvm.new.exe"
    Abort "staged pvm.exe does not report version ${VERSION}"
  ${EndIf}
  ; Not run to verify: pvm-desktop.exe has no command line and would open
  ; its window.
  File "/oname=pvm-desktop.new.exe" "${DESKTOP_EXE}"

  ; pvm looks for the Xpra client in xpra\ next to pvm.exe.
  DetailPrint "Staging Xpra client..."
  RMDir /r "$INSTDIR\.xpra.new"
  SetOutPath "$INSTDIR\.xpra.new"
  File /r "${XPRA_DIR}\*.*"
  ; Leave the staged directory: Windows cannot rename a process's cwd.
  SetOutPath "$INSTDIR"
  nsExec::ExecToStack /OEM '"$INSTDIR\.xpra.new\Xpra_cmd.exe" --version'
  Pop $1
  Pop $2
  ${If} $1 != 0
  ${OrIfNot} ${FileExists} "$INSTDIR\.xpra.new\Xpra.exe"
    RMDir /r "$INSTDIR\.xpra.new"
    Delete "$INSTDIR\pvm-desktop.new.exe"
    Delete "$INSTDIR\pvm.new.exe"
    Abort "staged Xpra_cmd.exe failed to run (exit $1)"
  ${EndIf}

  ; Swap the Xpra client, keeping the previous one until pvm.exe is in place.
  RMDir /r "$INSTDIR\.xpra.old"
  ${If} ${FileExists} "$INSTDIR\xpra\*.*"
    !insertmacro RenameRetry "$INSTDIR\xpra" "$INSTDIR\.xpra.old"
    ${If} ${Errors}
      RMDir /r "$INSTDIR\.xpra.new"
      Delete "$INSTDIR\pvm-desktop.new.exe"
      Delete "$INSTDIR\pvm.new.exe"
      Abort "cannot replace $INSTDIR\xpra (is an Xpra window still open?)"
    ${EndIf}
  ${EndIf}
  !insertmacro RenameRetry "$INSTDIR\.xpra.new" "$INSTDIR\xpra"
  ${If} ${Errors}
    Rename "$INSTDIR\.xpra.old" "$INSTDIR\xpra"
    RMDir /r "$INSTDIR\.xpra.new"
    Delete "$INSTDIR\pvm-desktop.new.exe"
    Delete "$INSTDIR\pvm.new.exe"
    Abort "cannot move the new Xpra client into $INSTDIR\xpra; the previous version was restored"
  ${EndIf}

  ; Swap PVM Desktop the same way.
  Delete "$INSTDIR\pvm-desktop.old.exe"
  ${If} ${FileExists} "$INSTDIR\pvm-desktop.exe"
    !insertmacro RenameRetry "$INSTDIR\pvm-desktop.exe" "$INSTDIR\pvm-desktop.old.exe"
    ${If} ${Errors}
      RMDir /r "$INSTDIR\xpra"
      Rename "$INSTDIR\.xpra.old" "$INSTDIR\xpra"
      Delete "$INSTDIR\pvm-desktop.new.exe"
      Delete "$INSTDIR\pvm.new.exe"
      Abort "cannot replace $INSTDIR\pvm-desktop.exe (is PVM Desktop still open?)"
    ${EndIf}
  ${EndIf}
  !insertmacro RenameRetry "$INSTDIR\pvm-desktop.new.exe" "$INSTDIR\pvm-desktop.exe"
  ${If} ${Errors}
    Rename "$INSTDIR\pvm-desktop.old.exe" "$INSTDIR\pvm-desktop.exe"
    RMDir /r "$INSTDIR\xpra"
    Rename "$INSTDIR\.xpra.old" "$INSTDIR\xpra"
    Delete "$INSTDIR\pvm-desktop.new.exe"
    Delete "$INSTDIR\pvm.new.exe"
    Abort "cannot move the new pvm-desktop.exe into place; the previous version was restored"
  ${EndIf}

  Delete "$INSTDIR\pvm.old.exe"
  ${If} ${FileExists} "$INSTDIR\pvm.exe"
    !insertmacro RenameRetry "$INSTDIR\pvm.exe" "$INSTDIR\pvm.old.exe"
    ${If} ${Errors}
      RMDir /r "$INSTDIR\xpra"
      Rename "$INSTDIR\.xpra.old" "$INSTDIR\xpra"
      Delete "$INSTDIR\pvm-desktop.exe"
      Rename "$INSTDIR\pvm-desktop.old.exe" "$INSTDIR\pvm-desktop.exe"
      Delete "$INSTDIR\pvm.new.exe"
      Abort "cannot replace $INSTDIR\pvm.exe (is pvm still running?)"
    ${EndIf}
  ${EndIf}
  !insertmacro RenameRetry "$INSTDIR\pvm.new.exe" "$INSTDIR\pvm.exe"
  ${If} ${Errors}
    Rename "$INSTDIR\pvm.old.exe" "$INSTDIR\pvm.exe"
    RMDir /r "$INSTDIR\xpra"
    Rename "$INSTDIR\.xpra.old" "$INSTDIR\xpra"
    Delete "$INSTDIR\pvm-desktop.exe"
    Rename "$INSTDIR\pvm-desktop.old.exe" "$INSTDIR\pvm-desktop.exe"
    Delete "$INSTDIR\pvm.new.exe"
    Abort "cannot move the new pvm.exe into place; the previous version was restored"
  ${EndIf}
  Delete "$INSTDIR\pvm.old.exe"
  Delete "$INSTDIR\pvm-desktop.old.exe"
  RMDir /r "$INSTDIR\.xpra.old"
  DetailPrint "Installed $INSTDIR\pvm.exe, $INSTDIR\pvm-desktop.exe and $INSTDIR\xpra"

  DetailPrint "Adding $INSTDIR to the user PATH..."
  !insertmacro UserPath add "$INSTDIR"

  WriteUninstaller "$INSTDIR\uninstall.exe"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "pvm"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  ; v1.4.0 recorded where it placed QEMU; this installer places none.
  DeleteRegValue HKCU "${UNINST_KEY}" "RuntimeRoot"
  WriteRegStr HKCU "${UNINST_KEY}" "PvmHome" "$PvmHome"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\pvm.exe"
  WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKCU "${UNINST_KEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoRepair" 1

  ClearErrors
  CreateShortcut "${DESKTOP_LNK}" "$INSTDIR\pvm-desktop.exe"
  ${If} ${Errors}
    Abort "cannot create the Start menu shortcut ${DESKTOP_LNK}"
  ${EndIf}
SectionEnd

; Removes only what the installer placed, and the value PVM Desktop writes
; to open at login. VM disks, keys, config and the QEMU builds under ~/.pvm
; stay: kept VMs and their snapshots are bound to those builds.
Section "Uninstall"
  ReadRegStr $PvmHome HKCU "${UNINST_KEY}" "PvmHome"
  ${If} $PvmHome == ""
    Abort "the pvm data folder is not recorded; run the pvm installer again, then uninstall"
  ${EndIf}
  Call un.EnsureStopped

  !insertmacro UserPath remove "$INSTDIR"

  Delete "${DESKTOP_LNK}"
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "pvm-desktop"
  Delete "$INSTDIR\pvm-desktop.exe"
  Delete "$INSTDIR\pvm.exe"
  RMDir /r "$INSTDIR\xpra"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  DeleteRegKey HKCU "${UNINST_KEY}"
  DetailPrint "VM data and QEMU builds under $PvmHome were kept; the builds are in $PvmHome\runtime\qemu\builds. Delete them manually if no longer needed."
SectionEnd
