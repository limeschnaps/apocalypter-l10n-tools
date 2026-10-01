@echo off
rem Installs a localization package into Apocalypter. Lists the *.lang files
rem next to this script, asks for one and runs patcher on the game. The game
rem folder (the one with Apocalypter_Data) is this script's folder or its
rem parent, so the archive can be unpacked either way.
setlocal EnableDelayedExpansion
cd /d "%~dp0"

set "data="
if exist "Apocalypter_Data\" (
  set "data=Apocalypter_Data"
) else if exist "..\Apocalypter_Data\" (
  set "data=..\Apocalypter_Data"
)
if not defined data (
  echo Apocalypter_Data not found. Put these files into the folder with Apocalypter.exe.
  goto end
)

echo Localization packages:
set count=0
for %%f in (*.lang) do (
  set /a count+=1
  set "pkg!count!=%%f"
  echo   !count!^) %%~nf
)
if %count%==0 (
  echo No .lang files found next to this script.
  goto end
)

:ask
set "choice="
set /p "choice=Select a package [1-%count%, Enter = 1]: "
if not defined choice set choice=1
set "valid=1"
for /f "delims=0123456789" %%x in ("!choice!") do set "valid="
if defined valid if !choice! geq 1 if !choice! leq %count% goto run
echo Enter a number from 1 to %count%.
goto ask

:run
set "pkg=!pkg%choice%!"
patcher.exe -package "!pkg!" -in-place "!data!"

:end
pause
