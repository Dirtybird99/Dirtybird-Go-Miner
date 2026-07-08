@echo off
REM Dirtybird Go Miner -- launcher (Windows).
REM Your settings live in config.json next to this file. You can edit config.json
REM directly, OR answer "y" below to set pool/wallet/threads interactively -- either way
REM persists to the same config.json that the miner reads. Double-click to run.
setlocal
cd /d "%~dp0"

set "BIN=go-miner.exe"
if not exist "%BIN%" (
    echo error: go-miner.exe not found. Run this from a release folder ^(next to go-miner.exe^).
    pause
    exit /b 1
)

set /p EDIT=Change pool/wallet/threads? (y/N):
if /i "%EDIT%"=="y" "%BIN%" --setup

echo.
echo Starting miner (Ctrl-C to stop)...
echo.
"%BIN%"
