; SPDX-License-Identifier: GPL-3.0-or-later
;
; Aether Time installer for Windows (x64 and ARM64).
;
; Built by scripts/build.sh with:
;   makensis -DVERSION=1.2.3 -DVERSION_NUM=1.2.3.0 -DEXE_AMD64=... [-DEXE_ARM64=...] -DOUTFILE=... installer/aether-time.nsi
;
; Silent install: AetherTime-Setup.exe /S [/D=C:\path]
; Silent uninstall: "C:\Program Files\AetherTime\uninstall.exe" /S

Unicode true
ManifestDPIAware true
SetCompressor /SOLID lzma
RequestExecutionLevel admin

!ifndef VERSION
  !define VERSION "0.0.0-dev"
!endif
!ifndef VERSION_NUM
  !define VERSION_NUM "0.0.0.0"
!endif
!ifndef OUTFILE
  !define OUTFILE "AetherTime-Setup-${VERSION}.exe"
!endif
!ifndef EXE_AMD64
  !error "EXE_AMD64 must point to the x64 aether-time.exe"
!endif

!define APP "Aether Time"
!define EXE "aether-time.exe"
!define PUBLISHER "KK4ODA"
!define URL "https://github.com/KK4ODA/Aether-Time"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\AetherTime"
!define RUN_KEY "Software\Microsoft\Windows\CurrentVersion\Run"

Name "${APP}"
OutFile "${OUTFILE}"
InstallDir "$PROGRAMFILES64\AetherTime"
InstallDirRegKey HKLM "${UNINST_KEY}" "InstallLocation"
BrandingText "${APP} ${VERSION}"

VIProductVersion "${VERSION_NUM}"
VIAddVersionKey "ProductName" "${APP}"
VIAddVersionKey "FileDescription" "${APP} setup"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "CompanyName" "${PUBLISHER}"
VIAddVersionKey "LegalCopyright" "Copyright (C) 2026 KK4ODA and contributors. GPL-3.0-or-later."

!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "x64.nsh"
!include "FileFunc.nsh"

; ---------- pages ----------
!define MUI_ICON "aether-time.ico"
!define MUI_UNICON "aether-time.ico"
!define MUI_WELCOMEFINISHPAGE_BITMAP "sidebar.bmp"
!define MUI_UNWELCOMEFINISHPAGE_BITMAP "sidebar.bmp"
!define MUI_ABORTWARNING
!define MUI_COMPONENTSPAGE_NODESC

!define MUI_WELCOMEPAGE_TITLE "Install ${APP} ${VERSION}"
!define MUI_WELCOMEPAGE_TEXT "${APP} keeps this computer's clock exact for FT8, FT4 and other digital modes, using internet time, a GPS receiver or the timing of your decodes, online or offline.$\r$\n$\r$\nIt runs as a Windows service so it can set the clock, and shows its status in the notification area."
!insertmacro MUI_PAGE_WELCOME

!define MUI_LICENSEPAGE_TEXT_TOP "${APP} is free software under the GNU General Public License."
!define MUI_LICENSEPAGE_TEXT_BOTTOM "You don't need to accept this license to use the program. It sets out your rights to share and change it."
!define MUI_LICENSEPAGE_BUTTON "$(^NextBtn)"
!insertmacro MUI_PAGE_LICENSE "../LICENSE"

!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES

!define MUI_FINISHPAGE_TITLE "${APP} is installed"
!define MUI_FINISHPAGE_TEXT "The service has started and is keeping the clock. Its icon is in the notification area; the dashboard is at http://127.0.0.1:8123/."
!define MUI_FINISHPAGE_RUN
!define MUI_FINISHPAGE_RUN_TEXT "Open the dashboard"
!define MUI_FINISHPAGE_RUN_FUNCTION OpenApp
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

; ---------- helpers ----------

; Stops the service and every running copy (the tray icons) so files can be replaced.
!macro StopEverything
  ${If} ${FileExists} "$INSTDIR\${EXE}"
    nsExec::ExecToLog '"$INSTDIR\${EXE}" service stop'
    Pop $0
  ${EndIf}
  nsExec::ExecToLog '"$SYSDIR\taskkill.exe" /F /IM ${EXE}'
  Pop $0
  Sleep 1500
!macroend

Function .onInit
  ${IfNot} ${RunningX64}
    MessageBox MB_OK|MB_ICONSTOP "${APP} needs 64-bit Windows (x64 or ARM64)." /SD IDOK
    Abort
  ${EndIf}
  SetRegView 64
FunctionEnd

Function un.onInit
  SetRegView 64
FunctionEnd

; Starts the program as the logged-in user rather than as administrator:
; with the service running it shows the tray icon and opens the dashboard.
Function OpenApp
  Exec '"$WINDIR\explorer.exe" "$INSTDIR\${EXE}"'
FunctionEnd

; ---------- sections ----------

Section "${APP} (required)" SecMain
  SectionIn RO
  SetShellVarContext all
  DetailPrint "Stopping ${APP} if it is running"
  !insertmacro StopEverything
  ; A program file that is still locked can be moved aside but not overwritten.
  Delete "$INSTDIR\${EXE}.old"
  ${If} ${FileExists} "$INSTDIR\${EXE}"
    Rename "$INSTDIR\${EXE}" "$INSTDIR\${EXE}.old"
  ${EndIf}

  SetOutPath "$INSTDIR"
  !ifdef EXE_ARM64
    ${If} ${IsNativeARM64}
      File "/oname=${EXE}" "${EXE_ARM64}"
    ${Else}
      File "/oname=${EXE}" "${EXE_AMD64}"
    ${EndIf}
  !else
    File "/oname=${EXE}" "${EXE_AMD64}"
  !endif
  File "../LICENSE"
  File "../README.md"
  File "../THIRD_PARTY_NOTICES.md"
  Delete /REBOOTOK "$INSTDIR\${EXE}.old"
  WriteUninstaller "$INSTDIR\uninstall.exe"

  CreateShortcut "$SMPROGRAMS\${APP}.lnk" "$INSTDIR\${EXE}" "" "$INSTDIR\${EXE}" 0
  WriteINIStr "$SMPROGRAMS\${APP} wall clock.url" "InternetShortcut" "URL" "http://127.0.0.1:8123/clock"

  WriteRegStr HKLM "${UNINST_KEY}" "DisplayName" "${APP}"
  WriteRegStr HKLM "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "${UNINST_KEY}" "Publisher" "${PUBLISHER}"
  WriteRegStr HKLM "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\${EXE}"
  WriteRegStr HKLM "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "${UNINST_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKLM "${UNINST_KEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegStr HKLM "${UNINST_KEY}" "URLInfoAbout" "${URL}"
  WriteRegStr HKLM "${UNINST_KEY}" "HelpLink" "${URL}#readme"
  WriteRegDWORD HKLM "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKLM "${UNINST_KEY}" "NoRepair" 1
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD HKLM "${UNINST_KEY}" "EstimatedSize" "$0"
SectionEnd

Section "Desktop shortcut" SecDesktop
  SetShellVarContext all
  CreateShortcut "$DESKTOP\${APP}.lnk" "$INSTDIR\${EXE}" "" "$INSTDIR\${EXE}" 0
SectionEnd

Section "Show the tray icon when I log in" SecLogin
  ; Same entry the tray's own "Start at login" option manages.
  WriteRegStr HKCU "${RUN_KEY}" "AetherTime" '"$INSTDIR\${EXE}" run --no-browser'
SectionEnd

Section "Stop Windows Time from also setting the clock" SecW32
  DetailPrint "Setting the Windows Time service to manual"
  nsExec::ExecToLog '"$SYSDIR\sc.exe" config w32time start= demand'
  Pop $0
  nsExec::ExecToLog '"$SYSDIR\net.exe" stop w32time'
  Pop $0
  WriteRegDWORD HKLM "${UNINST_KEY}" "StoppedWindowsTime" 1
SectionEnd

; Runs last: (re)install and start the service with the new program file.
Section "-Service"
  DetailPrint "Installing the ${APP} service"
  nsExec::ExecToLog '"$INSTDIR\${EXE}" service uninstall'
  Pop $0
  Sleep 1500
  nsExec::ExecToLog '"$INSTDIR\${EXE}" service install --start'
  Pop $0
  ${If} $0 != 0
    Sleep 3000
    nsExec::ExecToLog '"$INSTDIR\${EXE}" service install --start'
    Pop $0
  ${EndIf}
  ${If} $0 != 0
    MessageBox MB_OK|MB_ICONEXCLAMATION "The ${APP} service could not be started. Restart Windows and run the installer again; until then the program can only measure the clock." /SD IDOK
  ${EndIf}
SectionEnd

Section "Uninstall"
  SetShellVarContext all
  DetailPrint "Removing the ${APP} service"
  nsExec::ExecToLog '"$INSTDIR\${EXE}" service stop'
  Pop $0
  nsExec::ExecToLog '"$INSTDIR\${EXE}" service uninstall'
  Pop $0
  nsExec::ExecToLog '"$SYSDIR\taskkill.exe" /F /IM ${EXE}'
  Pop $0
  Sleep 1500

  Delete /REBOOTOK "$INSTDIR\${EXE}"
  Delete /REBOOTOK "$INSTDIR\${EXE}.old"
  Delete "$INSTDIR\LICENSE"
  Delete "$INSTDIR\README.md"
  Delete "$INSTDIR\THIRD_PARTY_NOTICES.md"
  Delete "$INSTDIR\uninstall.exe"
  RMDir /REBOOTOK "$INSTDIR"

  Delete "$SMPROGRAMS\${APP}.lnk"
  Delete "$SMPROGRAMS\${APP} wall clock.url"
  Delete "$DESKTOP\${APP}.lnk"
  DeleteRegValue HKCU "${RUN_KEY}" "AetherTime"

  ; Give the clock back to Windows if the installer took it.
  ReadRegDWORD $0 HKLM "${UNINST_KEY}" "StoppedWindowsTime"
  ${If} $0 == 1
    nsExec::ExecToLog '"$SYSDIR\net.exe" start w32time'
    Pop $0
  ${EndIf}
  DeleteRegKey HKLM "${UNINST_KEY}"

  ; $APPDATA is C:\ProgramData here (all users).
  MessageBox MB_YESNO|MB_ICONQUESTION "Also delete ${APP}'s settings, logs and learned values?" /SD IDNO IDNO keep
    RMDir /r "$APPDATA\AetherTime"
  keep:
SectionEnd
