export interface FileEntry {
  name: string
  type: 'file' | 'directory' | 'link' | 'special'
  executable?: boolean
  has_error?: boolean
}

export function fileEntryKind(entry: FileEntry) {
  if (entry.has_error) return 'error'
  if (entry.type === 'directory') return 'directory'
  if (entry.type === 'link') return 'link'
  if (entry.type === 'file') {
    if (entry.executable) return 'executable'
    if (/\.(?:zip|zipx|7z|rar|tar|gz|gzip|bz2|bzip2|xz|zst|zstd|tgz|tbz2?|txz|tzst|lz|lzma|lz4|lzo|Z|jar|war|ear|ar|deb|rpm|cab|iso)$/i.test(entry.name)) return 'archive'
  }
  return 'file'
}
