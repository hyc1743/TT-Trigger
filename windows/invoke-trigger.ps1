param(
    [Parameter(Mandatory=$true)][string]$BaseUrl,
    [string]$KeyId = $env:TT_KEY_ID,
    [string]$Secret = $env:TT_HMAC_SECRET,
    [Parameter(Mandatory=$true)][string]$Symbol,
    [switch]$AddPair
)

$ErrorActionPreference = 'Stop'
if (-not $KeyId) { throw '请通过 -KeyId 或 TT_KEY_ID 提供 keyId。' }
if (-not $Secret) { throw '请通过 -Secret 或 TT_HMAC_SECRET 提供 HMAC secret。' }

function From-Base64Url([string]$Value) {
    $text = $Value.Replace('-','+').Replace('_','/')
    while (($text.Length % 4) -ne 0) { $text += '=' }
    return [Convert]::FromBase64String($text)
}
function To-Base64Url([byte[]]$Value) {
    return [Convert]::ToBase64String($Value).TrimEnd('=').Replace('+','-').Replace('/','_')
}

$requestId = [Guid]::NewGuid().ToString()
$payload = [ordered]@{ requestId = $requestId; symbol = $Symbol; addPair = [bool]$AddPair }
$json = $payload | ConvertTo-Json -Compress
$body = [Text.Encoding]::UTF8.GetBytes($json)
$timestamp = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
$nonceBytes = New-Object byte[] 16
$rng = [Security.Cryptography.RandomNumberGenerator]::Create()
$rng.GetBytes($nonceBytes); $rng.Dispose()
$nonce = To-Base64Url $nonceBytes
$sha = [Security.Cryptography.SHA256]::Create()
$bodyHash = (($sha.ComputeHash($body) | ForEach-Object { $_.ToString('x2') }) -join '')
$sha.Dispose()
$canonical = "TT-TRIGGER-V1`nPOST`n/webhook`n$timestamp`n$nonce`n$bodyHash"
$hmac = New-Object Security.Cryptography.HMACSHA256
$hmac.Key = From-Base64Url $Secret
$signature = (($hmac.ComputeHash([Text.Encoding]::ASCII.GetBytes($canonical)) | ForEach-Object { $_.ToString('x2') }) -join '')
$hmac.Dispose()

$uri = $BaseUrl.TrimEnd('/') + '/webhook'
$request = [Net.HttpWebRequest]::Create($uri)
$request.Method = 'POST'
$request.ContentType = 'application/json'
$request.Headers.Add('X-TT-Key-Id', $KeyId)
$request.Headers.Add('X-TT-Timestamp', [string]$timestamp)
$request.Headers.Add('X-TT-Nonce', $nonce)
$request.Headers.Add('X-TT-Signature', $signature)
$request.ContentLength = $body.Length
$stream = $request.GetRequestStream(); $stream.Write($body, 0, $body.Length); $stream.Dispose()
try { $response = $request.GetResponse() }
catch [Net.WebException] { if ($_.Exception.Response) { $response = $_.Exception.Response } else { throw } }
$reader = New-Object IO.StreamReader($response.GetResponseStream(), [Text.Encoding]::UTF8)
$responseBody = $reader.ReadToEnd(); $reader.Dispose()
Write-Host "HTTP $([int]$response.StatusCode)"
Write-Output $responseBody
if ([int]$response.StatusCode -lt 200 -or [int]$response.StatusCode -ge 300) { exit 1 }
