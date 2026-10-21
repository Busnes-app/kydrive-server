import { useState } from 'react';
import { File, FileArchive, FileAudio, FileCode, FileImage, FileSpreadsheet, FileText, FileVideo, Folder, PenTool, Presentation } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { extension } from './client';

const kinds: [LucideIcon, string[]][] = [
  [FileImage, ['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'svg', 'heic', 'tif', 'tiff']],
  [FileText, ['pdf', 'docx', 'doc', 'odt', 'rtf', 'md', 'markdown', 'txt', 'log']],
  [FileSpreadsheet, ['xlsx', 'xls', 'ods', 'csv', 'tsv']],
  [Presentation, ['pptx', 'ppt', 'odp', 'key']],
  [PenTool, ['excalidraw']],
  [FileAudio, ['mp3', 'wav', 'ogg', 'oga', 'm4a', 'flac']],
  [FileVideo, ['mp4', 'webm', 'mov', 'mkv', 'ogv']],
  [FileArchive, ['zip', 'gz', 'tgz', '7z', 'rar', 'tar']],
  [FileCode, ['html', 'htm', 'css', 'js', 'ts', 'json', 'xml', 'yaml', 'yml', 'go', 'py', 'sh']],
];

// The server thumbnails raster images itself. A refusal (a misnamed file) just keeps the icon.
// Documents keep their type icon: a 32 px render of a text page is a blank white tile.
const thumbnailed = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp']);

export function iconFor(name: string): LucideIcon {
  const ext = extension(name);
  return kinds.find(([, exts]) => exts.includes(ext))?.[0] ?? File;
}

// FileIcon shows a file's type icon, or an image's thumbnail once one loads. The revision in the
// URL fetches a new thumbnail after a new version.
export function FileIcon({ file }: { file: { id: string; name: string; revision?: number } }) {
  const [failed, setFailed] = useState(false);
  if (thumbnailed.has(extension(file.name)) && !failed) {
    return <img className="drive-file-icon drive-thumbnail" src={`/api/drive/files/${encodeURIComponent(file.id)}/thumbnail?r=${file.revision ?? 0}`} alt="" loading="lazy" onError={() => setFailed(true)} />;
  }
  const Icon = iconFor(file.name);
  return <Icon className="drive-file-icon" aria-hidden="true" />;
}

export function FolderIcon() {
  return <Folder className="drive-file-icon folder" aria-hidden="true" />;
}
