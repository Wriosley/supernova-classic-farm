param(
    [string]$BaseUrl = 'http://127.0.0.1:8080',
    [string]$MailTitle = 'Hello',
    [string]$MailContent = 'I visited your farm.',
    [switch]$MailOnly
)
$ErrorActionPreference = 'Stop'

function Post-Json($Path, $Body, $Token = '') {
    $headers = @{}
    if ($Token) { $headers.Authorization = "Bearer $Token" }
    Invoke-RestMethod -Method Post -Uri "$BaseUrl$Path" -Headers $headers -ContentType 'application/json' -Body ($Body | ConvertTo-Json -Compress)
}

function Send-Ws($Socket, $Object) {
    $text = $Object | ConvertTo-Json -Compress
    $bytes = [Text.Encoding]::UTF8.GetBytes($text)
    [void]$Socket.SendAsync([ArraySegment[byte]]::new($bytes), 'Text', $true, [Threading.CancellationToken]::None).GetAwaiter().GetResult()
}

function Read-Ws($Socket) {
    $buffer = New-Object byte[] 65536
    $result = $Socket.ReceiveAsync([ArraySegment[byte]]::new($buffer), [Threading.CancellationToken]::None).GetAwaiter().GetResult()
    ([Text.Encoding]::UTF8.GetString($buffer, 0, $result.Count)) | ConvertFrom-Json
}

function Connect-Ws($Token) {
    $newSocket = [Net.WebSockets.ClientWebSocket]::new()
    [void]$newSocket.ConnectAsync([Uri]($BaseUrl.Replace('http','ws') + '/ws'), [Threading.CancellationToken]::None).GetAwaiter().GetResult()
    Send-Ws $newSocket @{request_id='auth0001'; action='AUTH'; data=@{token=$Token}}
    [void](Read-Ws $newSocket)
    Write-Output -NoEnumerate $newSocket
}

$suffix = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
$firstName = "demoa$suffix"
$secondName = "demob$suffix"
$password = 'Farm1234'
$first = Post-Json '/api/register' @{username=$firstName; password=$password}
$second = Post-Json '/api/register' @{username=$secondName; password=$password}
$firstLogin = Post-Json '/api/login' @{username=$firstName; password=$password}
$secondLogin = Post-Json '/api/login' @{username=$secondName; password=$password}

$added = Post-Json '/api/friends/add' @{username=$secondName} $firstLogin.token
$friends = Invoke-RestMethod -Uri "$BaseUrl/api/friends" -Headers @{Authorization="Bearer $($firstLogin.token)"}
$farm = Invoke-RestMethod -Uri "$BaseUrl/api/friends/$($second.player_id)/farm" -Headers @{Authorization="Bearer $($firstLogin.token)"}
Write-Host "Friend added: $($added.friend_id); friends: $($friends.friends.Count); plots: $($farm.plots.Count)"
if (-not $MailOnly) {
    $socket = Connect-Ws $secondLogin.token
    Send-Ws $socket @{request_id='buy00001'; action='BUY_SEEDS'; data=@{crop_id=1; quantity=1}}
    [void](Read-Ws $socket)
    Send-Ws $socket @{request_id='plant001'; action='PLANT'; data=@{plot_id=1; crop_id=1}}
    [void](Read-Ws $socket)
    Send-Ws $socket @{request_id='fert0001'; action='APPLY_FERTILIZER'; data=@{plot_id=1}}
    [void](Read-Ws $socket)
    Write-Host 'Waiting 21 seconds for the fertilized carrot...'
    Start-Sleep -Seconds 21
    $stolen = Post-Json "/api/friends/$($second.player_id)/steal" @{plot_id=1} $firstLogin.token
    $carrot = $stolen.snapshot.inventory | Where-Object crop_id -eq 1
    Write-Host "Steal succeeded: coins=$($stolen.snapshot.coins); carrots=$($carrot.crop_count)"
    $socket.Dispose()
}
$mailResponse = Post-Json "/api/friends/$($second.player_id)/mail" @{title=$MailTitle; content=$MailContent} $firstLogin.token
$received = $mailResponse.mails | Where-Object { $_.title -eq $MailTitle -and $_.content -eq $MailContent } | Select-Object -First 1
if (-not $received) {
    Write-Host ($mailResponse | ConvertTo-Json -Depth 5)
    throw 'The server inserted the mail, but could not read it back from B mailbox.'
}
Write-Host "B received mail: title=[$($received.title)] content=[$($received.content)]"
