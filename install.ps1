$ErrorActionPreference = "Stop"

$repo = "General-Specialist/capabot"
$binary = "capabot"

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$installDir = "$env:LOCALAPPDATA\Microsoft\WindowsApps"

$release = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
$tag = $release.tag_name

$filename = "${binary}_windows_${arch}.zip"
$url = "https://github.com/$repo/releases/download/$tag/$filename"

Write-Host "Downloading $binary $tag for windows/$arch..."

$tmp = New-TemporaryFile | Rename-Item -NewName { $_.Name + ".zip" } -PassThru
try {
    Invoke-WebRequest -Uri $url -OutFile $tmp.FullName
    Expand-Archive -Path $tmp.FullName -DestinationPath $tmp.DirectoryName -Force

    $src = Join-Path $tmp.DirectoryName "${binary}.exe"
    $dest = Join-Path $installDir "${binary}.exe"
    Move-Item -Path $src -Destination $dest -Force

    Write-Host ""
    Write-Host "  ✓ $binary $tag installed to $installDir" -ForegroundColor Green
    Write-Host ""
    Write-Host "  Run 'capabot serve' to start the server."
    Write-Host "  Run 'capabot --help' for all commands."
    Write-Host ""
} finally {
    Remove-Item $tmp.FullName -ErrorAction SilentlyContinue
}
