@echo off
title a_gnome_trader
cd /d "%~dp0"
if not exist a_gnome_trader.exe (
    echo a_gnome_trader.exe not found. Download it from the GitHub release,
    echo or build it yourself:  cd src ^&^& go build -o ..\a_gnome_trader.exe .
    pause
    exit /b 1
)
if exist stop.flag del stop.flag
:loop
a_gnome_trader.exe
if exist stop.flag (
    del stop.flag
    exit
)
echo.
echo Bot exited. Restarting in 10 seconds... (close this window to stop)
timeout /t 10 /nobreak >nul
goto loop
