param([string]$BaseUrl = 'http://127.0.0.1:8080')
$ErrorActionPreference = 'Stop'

function Post-Json($Path, $Body) {
    Invoke-RestMethod -Method Post -Uri "$BaseUrl$Path" -ContentType 'application/json' -Body ($Body | ConvertTo-Json -Compress)
}

function Send-Ws($Socket, $Object) {
    $bytes = [Text.Encoding]::UTF8.GetBytes(($Object | ConvertTo-Json -Compress))
    [void]$Socket.SendAsync([ArraySegment[byte]]::new($bytes), 'Text', $true, [Threading.CancellationToken]::None).GetAwaiter().GetResult()
}

function Read-Ws($Socket) {
    $buffer = New-Object byte[] 65536
    $result = $Socket.ReceiveAsync([ArraySegment[byte]]::new($buffer), [Threading.CancellationToken]::None).GetAwaiter().GetResult()
    ([Text.Encoding]::UTF8.GetString($buffer, 0, $result.Count)) | ConvertFrom-Json
}

function Command($Socket, $Id, $Action, $Data) {
    Send-Ws $Socket @{request_id=$Id; action=$Action; data=$Data}
    $response = Read-Ws $Socket
    if ($response.code -ne 'OK') { throw "$Action failed: $($response.code) $($response.message)" }
    return $response
}

$username = "crops$([DateTimeOffset]::UtcNow.ToUnixTimeSeconds())"
$password = 'Farm1234'
[void](Post-Json '/api/register' @{username=$username; password=$password})
$login = Post-Json '/api/login' @{username=$username; password=$password}
$socket = [Net.WebSockets.ClientWebSocket]::new()
[void]$socket.ConnectAsync([Uri]($BaseUrl.Replace('http','ws') + '/ws'), [Threading.CancellationToken]::None).GetAwaiter().GetResult()
[void](Command $socket 'auth0001' 'AUTH' @{token=$login.token})

for ($crop = 1; $crop -le 6; $crop++) {
    [void](Command $socket "buy0000$crop" 'BUY_SEEDS' @{crop_id=$crop; quantity=1})
    [void](Command $socket "plant00$crop" 'PLANT' @{plot_id=$crop; crop_id=$crop})
}
Write-Host 'Six crops purchased and planted. Waiting 61 seconds...'
for ($wait = 1; $wait -le 3; $wait++) {
    Start-Sleep -Seconds 20
    [void](Command $socket "ping000$wait" 'PING' @{})
}
Start-Sleep -Seconds 1

for ($crop = 1; $crop -le 6; $crop++) {
    $harvest = Command $socket "harvest$crop" 'HARVEST' @{plot_id=$crop}
    $item = $harvest.snapshot.inventory | Where-Object crop_id -eq $crop
    $shop = $harvest.snapshot.shop | Where-Object crop_id -eq $crop
    if ($item.crop_count -ne $shop.yield) { throw "Crop $crop yield mismatch" }
    $sale = Command $socket "sell000$crop" 'SELL_CROP' @{crop_id=$crop; quantity=1}
    $after = $sale.snapshot.inventory | Where-Object crop_id -eq $crop
    if ($after.crop_count -ne ($shop.yield - 1)) { throw "Crop $crop sale mismatch" }
    Write-Host "crop_id=$crop name=$($shop.crop_name) yield=$($shop.yield) sale_price=$($shop.sale_price) OK"
}

$socket.Dispose()
Write-Host 'ALL_SIX_CROPS_OK'
