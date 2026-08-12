const BANNER_ID = 'fatal-error-banner'

// Rendered with raw DOM on purpose: once Vue has thrown out of a render it can
// no longer be trusted to paint anything, so the notice has to live outside it.
export function reportFatalError(error: unknown, info: string): void {
  if (typeof document === 'undefined') {
    return
  }
  const detail = error instanceof Error ? error.message : String(error)
  const existing = document.getElementById(BANNER_ID)
  const banner = existing ?? document.createElement('div')
  if (!existing) {
    banner.id = BANNER_ID
    banner.setAttribute('role', 'alert')
    banner.style.cssText = [
      'position:fixed',
      'inset:0 0 auto 0',
      'z-index:9999',
      'padding:0.75rem 1rem',
      'background:#8c2f2f',
      'color:#fff',
      'font:700 0.85rem/1.4 system-ui,sans-serif',
    ].join(';')
    document.body.appendChild(banner)
  }
  banner.textContent = `界面渲染出错，已停止刷新：${detail}（${info}）。请刷新页面；控制台有完整堆栈。`
}
