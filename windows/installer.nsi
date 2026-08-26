!include "MUI2.nsh"

Name "Timetable"
OutFile "../build/timetable-setup-x64.exe"
InstallDir "$PROGRAMFILES64\Timetable"
RequestExecutionLevel admin
SetCompressor /SOLID lzma

!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_LANGUAGE "Russian"

Section "Install"
  SetOutPath "$INSTDIR"
  File "staging/timetable.exe"
  File "staging/ortools_csat.dll"
  !ifexist "staging/ortools.dll"
    File "staging/ortools.dll"
  !endif
  !ifexist "staging/vcruntime140.dll"
    File "staging/vcruntime140.dll"
  !endif
  !ifexist "staging/vcruntime140_1.dll"
    File "staging/vcruntime140_1.dll"
  !endif
  !ifexist "staging/msvcp140.dll"
    File "staging/msvcp140.dll"
  !endif
  !ifexist "staging/msvcp140_1.dll"
    File "staging/msvcp140_1.dll"
  !endif
  !ifexist "staging/msvcp140_codecvt_ids.dll"
    File "staging/msvcp140_codecvt_ids.dll"
  !endif
  File "staging/MicrosoftEdgeWebView2RuntimeInstallerX64.exe"

  DetailPrint "Установка WebView2 Runtime (офлайн)..."
  ExecWait '"$INSTDIR\MicrosoftEdgeWebView2RuntimeInstallerX64.exe" /silent /install'

  CreateShortcut "$SMPROGRAMS\Timetable.lnk" "$INSTDIR\timetable.exe"
  CreateShortcut "$DESKTOP\Timetable.lnk" "$INSTDIR\timetable.exe"
  WriteUninstaller "$INSTDIR\uninstall.exe"
SectionEnd

Section "Uninstall"
  Delete "$SMPROGRAMS\Timetable.lnk"
  Delete "$DESKTOP\Timetable.lnk"
  Delete "$INSTDIR\timetable.exe"
  Delete "$INSTDIR\ortools.dll"
  Delete "$INSTDIR\ortools_csat.dll"
  Delete "$INSTDIR\vcruntime140.dll"
  Delete "$INSTDIR\vcruntime140_1.dll"
  Delete "$INSTDIR\msvcp140.dll"
  Delete "$INSTDIR\msvcp140_1.dll"
  Delete "$INSTDIR\msvcp140_codecvt_ids.dll"
  Delete "$INSTDIR\MicrosoftEdgeWebView2RuntimeInstallerX64.exe"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
SectionEnd
