import { createContext, useContext, type ReactNode } from 'react'

const dictionaries = {
  'zh-CN': {
    projects: '项目', newProject: '新建项目', untitled: '未命名画布', apiKey: 'API Key', model: '模型',
    createKey: '创建 API Key', prompt: '提示词', generate: '生成', edit: '编辑', generating: '生成中',
    addText: '添加文本', addImage: '上传图片', addGroup: '添加分组', addConfig: '添加配置', undo: '撤销', redo: '重做',
    zoomIn: '放大', zoomOut: '缩小', fit: '适应视图', import: '导入', export: '导出', save: '保存', saved: '已保存',
    saving: '保存中', conflict: '版本冲突', failed: '失败', partial: '部分完成', cancel: '取消', source: '关于与源码',
    sourceTitle: 'Infinite Canvas 来源', close: '关闭', delete: '删除', generation: '文生图', imageEdit: '图片编辑',
    noProject: '暂无画布项目', selectImage: '选择一个图片节点进行编辑', emptyKey: '需要一个可用的 API Key',
    emptyModel: '当前 Key 没有可用图片模型', policyFallback: '模型服务异常时会按管理员顺序回退', reload: '重新加载',
    saveCopy: '另存为新项目', completed: '已完成', canceled: '已取消', collapseProjects: '收起项目列表',
    showProjects: '显示项目列表', projectName: '项目名称', connect: '连接所选节点', operation: '生成模式', size: '尺寸',
    outputCount: '数量', textPlaceholder: '文本', groupDefault: '分组', nodeCancel: '取消任务', invalidDocument: '无效的画布文档',
    deleteProject: '删除项目', deleteProjectConfirm: '确定删除这个画布项目吗？此操作无法撤销。',
    unsupportedImage: '仅支持 PNG、JPEG 和 WebP 图片。', imageTooLarge: '图片不能超过 20 MB。',
    assetUnavailable: '图片资产在当前浏览器中不可用', exportAssetsWarning: '已导出画布结构；图片仍保存在当前浏览器中，不包含在 JSON 文件内。',
    nodeImage: '图片', nodeText: '文本', nodeConfig: '配置', nodeGroup: '分组', upstream: '上游项目', commit: '提交',
    license: '许可证', modified: '修改日期', warranty: '本程序不提供任何担保。模型请求通过已认证的 Sub2API 服务端处理；画布项目与图片保存在当前浏览器。',
    correspondingSource: '对应源码', conflictActions: '本地画布保存失败，请检查浏览器存储空间。'
  },
  en: {
    projects: 'Projects', newProject: 'New project', untitled: 'Untitled canvas', apiKey: 'API Key', model: 'Model',
    createKey: 'Create API key', prompt: 'Prompt', generate: 'Generate', edit: 'Edit', generating: 'Generating',
    addText: 'Add text', addImage: 'Upload image', addGroup: 'Add group', addConfig: 'Add config', undo: 'Undo', redo: 'Redo',
    zoomIn: 'Zoom in', zoomOut: 'Zoom out', fit: 'Fit view', import: 'Import', export: 'Export', save: 'Save', saved: 'Saved',
    saving: 'Saving', conflict: 'Version conflict', failed: 'Failed', partial: 'Partial', cancel: 'Cancel', source: 'About and source',
    sourceTitle: 'Infinite Canvas source', close: 'Close', delete: 'Delete', generation: 'Generate', imageEdit: 'Edit image',
    noProject: 'No canvas projects', selectImage: 'Select an image node to edit', emptyKey: 'A usable API key is required',
    emptyModel: 'No image model is available for this key', policyFallback: 'Service failures follow the administrator fallback order', reload: 'Reload',
    saveCopy: 'Save as new project', completed: 'Completed', canceled: 'Canceled', collapseProjects: 'Collapse projects',
    showProjects: 'Show projects', projectName: 'Project name', connect: 'Connect selected nodes', operation: 'Generation mode', size: 'Size',
    outputCount: 'Count', textPlaceholder: 'Text', groupDefault: 'Group', nodeCancel: 'Cancel job', invalidDocument: 'Invalid canvas document',
    deleteProject: 'Delete project', deleteProjectConfirm: 'Delete this canvas project? This cannot be undone.',
    unsupportedImage: 'Only PNG, JPEG, and WebP images are supported.', imageTooLarge: 'Images must be 20 MB or smaller.',
    assetUnavailable: 'This image asset is unavailable in the current browser', exportAssetsWarning: 'Canvas structure exported. Browser-stored images are not included in the JSON file.',
    nodeImage: 'Image', nodeText: 'Text', nodeConfig: 'Config', nodeGroup: 'Group', upstream: 'Upstream', commit: 'Commit',
    license: 'License', modified: 'Modified', warranty: 'This program is provided without warranty. Model requests use the authenticated Sub2API server; canvas projects and images stay in this browser.',
    correspondingSource: 'Corresponding source', conflictActions: 'Local canvas storage failed. Check the browser storage quota.'
  }
} as const

type Dictionary = Record<keyof typeof dictionaries.en, string>
type TranslationKey = keyof Dictionary
const I18nContext = createContext<Dictionary>(dictionaries.en)

export function CanvasI18nProvider({ locale, children }: { locale: string; children: ReactNode }) {
  const dictionary = locale.toLowerCase().startsWith('zh') ? dictionaries['zh-CN'] : dictionaries.en
  return <I18nContext.Provider value={dictionary}>{children}</I18nContext.Provider>
}

export function useCanvasI18n() {
  const dictionary = useContext(I18nContext)
  return (key: TranslationKey) => dictionary[key]
}

export { dictionaries }
