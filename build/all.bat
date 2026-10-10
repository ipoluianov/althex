@echo off
rem Builds althex for every supported platform into one bin\<yyyy-mm-dd-HH-mm>-<version>\.
setlocal
for /f %%t in ('powershell -NoProfile -Command "Get-Date -Format yyyy-MM-dd-HH-mm"') do set "BUILD_STAMP=%%t"
for %%t in (windows:amd64 linux:amd64 linux:arm64 darwin:arm64) do (
  for /f "tokens=1,2 delims=:" %%a in ("%%t") do (
    call "%~dp0_build.bat" %%a %%b || exit /b 1
  )
)
rem Checksums of the release files (6.3 of the spec), in the format of sha256sum
for /d %%d in ("%~dp0..\bin\%BUILD_STAMP%-*") do (
  powershell -NoProfile -Command "Get-ChildItem -File '%%d' | Where-Object Name -ne 'SHA256SUMS' | Sort-Object Name | ForEach-Object { (Get-FileHash -Algorithm SHA256 $_.FullName).Hash.ToLower() + '  ' + $_.Name } | Set-Content -Encoding ascii '%%d\SHA256SUMS'"
)
