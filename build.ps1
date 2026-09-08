# build.ps1

# Stop on errors
$ErrorActionPreference = "Stop"

$targetDir = "C:\Tools"
$outputFile = "dump.exe"

Write-Host "Building Go executable..." -ForegroundColor Cyan
go build -o $outputFile main.go

if ($LASTEXITCODE -ne 0) {
    Write-Error "Go build failed."
    exit $LASTEXITCODE
}

# Ensure the destination directory exists
if (-not (Test-Path -Path $targetDir)) {
    Write-Host "Directory $targetDir does not exist. Creating it..." -ForegroundColor Yellow
    New-Item -ItemType Directory -Path $targetDir | Out-Null
}

Write-Host "Copying $outputFile to $targetDir..." -ForegroundColor Cyan
Copy-Item -Path $outputFile -Destination $targetDir -Force

Write-Host "Build and deployment completed successfully!" -ForegroundColor Green