import { ExternalLink, X } from 'lucide-react'
import { useCanvasI18n } from '@sub2api/i18n'

export function SourceNoticeDialog({ open, onClose }: { open: boolean; onClose(): void }) {
  const t = useCanvasI18n()
  if (!open) return null
  return (
    <div className="canvas-modal-backdrop" role="presentation" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
      <section className="canvas-modal" role="dialog" aria-modal="true" aria-labelledby="canvas-source-title">
        <header>
          <h2 id="canvas-source-title">{t('sourceTitle')}</h2>
          <button className="icon-button" onClick={onClose} aria-label={t('close')} title={t('close')}><X size={18} /></button>
        </header>
        <dl>
          <dt>{t('upstream')}</dt><dd>basketikun/infinite-canvas</dd>
          <dt>{t('commit')}</dt><dd><code>ea0414e88cffa6b522cc13c0613b3c8085983a53</code></dd>
          <dt>{t('license')}</dt><dd>AGPL-3.0-only</dd>
          <dt>{t('modified')}</dt><dd>2026-08-21</dd>
        </dl>
        <p>{t('warranty')}</p>
        <a className="canvas-command primary" href={__CANVAS_SOURCE_URL__} target="_blank" rel="noreferrer">
          <ExternalLink size={16} /> {t('correspondingSource')}
        </a>
      </section>
    </div>
  )
}
