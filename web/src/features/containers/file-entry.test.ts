import { describe, expect, it } from 'vitest'
import { fileEntryKind } from './file-entry'

describe('container file presentation', () => {
  it('uses runtime attributes before filenames', () => {
    expect(fileEntryKind({ name: 'app', type: 'file', executable: true })).toBe('executable')
    expect(fileEntryKind({ name: 'script.sh', type: 'file' })).toBe('file')
    expect(fileEntryKind({ name: 'data.zip', type: 'directory' })).toBe('directory')
    expect(fileEntryKind({ name: 'app.tar.gz', type: 'link', executable: true })).toBe('link')
    expect(fileEntryKind({ name: 'run.zip', type: 'file', executable: true })).toBe('executable')
    expect(fileEntryKind({ name: 'broken', type: 'link', has_error: true })).toBe('error')
    expect(fileEntryKind({ name: 'unreadable', type: 'file', executable: true, has_error: true })).toBe('error')
    expect(fileEntryKind({ name: 'socket.zip', type: 'special' })).toBe('file')
  })

  it('recognizes archive suffixes without coloring similarly named plain files', () => {
    for (const name of ['backup.tar.gz', 'backup.TGZ', 'app.zip', 'archive.7z', 'archive.tar.zst', 'app.jar', 'app.deb']) {
      expect(fileEntryKind({ name, type: 'file' }), name).toBe('archive')
    }
    for (const name of ['archive.zip.txt', 'zip', '.env', 'config.json', 'archive.tgz.bak']) {
      expect(fileEntryKind({ name, type: 'file' }), name).toBe('file')
    }
  })
})
