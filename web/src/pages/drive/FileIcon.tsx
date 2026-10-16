import { useState } from 'react';
import { File, FileAudio, FileImage, FileSpreadsheet, FileText, FileVideo, PenTool, Presentation } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { extension } from './client';

const kinds: [LucideIcon, string[]][] = [
  [FileImage, ['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'svg', 'heic', 'tif', 'tiff']],
  [FileText, ['pdf', 'docx', 'doc', 'odt', 'rtf', 'md', 'txt']],
  [FileSpreadsheet, ['xlsx', 'xls', 'ods', 'csv']],
  [Presentation, ['pptx', 'ppt', 'odp']],
  [PenTool, ['excalidraw']],
  [FileAudio, ['mp3', 'wav', 'ogg', 'oga', 'm4a', 'flac']],
  [FileVideo, ['mp4', 'webm', 'mov', 'mkv', 'ogv']],
];

// The server thumbnails these: images itself, documents through Euro-Office when it is
// configured. A refusal (a misnamed file, no Euro-Office) just keeps the icon.
const thumbnailed = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'pdf', 'docx', 'odt', 'rtf', 'xlsx', 'ods', 'csv', 'pptx', 'odp']);

// FileIcon shows a file's type icon, or its thumbnail once one loads. The revision in the URL
// fetches a new thumbnail after a new version.
export function FileIcon({ file }: { file: { id: string; name: string; revision?: number } }) {
  const ext = extension(file.name);
  const [failed, setFailed] = useState(false);
  if (thumbnailed.has(ext) && !failed) {
    return <img className="drive-file-icon drive-thumbnail" src={`/api/drive/files/${encodeURIComponent(file.id)}/thumbnail?r=${file.revision ?? 0}`} alt="" loading="lazy" onError={() => setFailed(true)} />;
  }
  const Icon = kinds.find(([, exts]) => exts.includes(ext))?.[0] ?? File;
  return <Icon className="drive-file-icon" aria-hidden="true" />;
}
