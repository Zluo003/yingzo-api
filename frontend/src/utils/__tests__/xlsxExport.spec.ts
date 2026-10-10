import { describe, expect, it } from 'vitest'
import { read, utils, write } from 'xlsx'

describe('usage workbook export compatibility', () => {
  it('preserves paginated Chinese headers, timestamps and numeric costs in a real XLSX file', () => {
    const headers = ['时间', '用户', '模型', '费用', '输入 Token']
    const firstPage = [['2026-10-10T10:00:00Z', '创作者@example.com', 'image-model', 0.000001, 0]]
    const secondPage = [['2026-10-10T11:00:00Z', 'user@example.com', 'text-model', 123.456789, 100]]
    const sheet = utils.aoa_to_sheet([headers])
    utils.sheet_add_aoa(sheet, firstPage, { origin: -1 })
    utils.sheet_add_aoa(sheet, secondPage, { origin: -1 })
    const workbook = utils.book_new()
    utils.book_append_sheet(workbook, sheet, 'Usage')

    const bytes = write(workbook, { bookType: 'xlsx', type: 'array' })
    const decoded = read(bytes, { type: 'array' })

    expect(decoded.SheetNames).toEqual(['Usage'])
    expect(utils.sheet_to_json(decoded.Sheets.Usage!, { header: 1 })).toEqual([
      headers, ...firstPage, ...secondPage,
    ])
  })

  it('keeps user-controlled text as text instead of introducing spreadsheet formulas', () => {
    const values = ['=SUM(A1:A2)', '+123', '@username', '中文\n多行内容']
    const sheet = utils.aoa_to_sheet([values])
    const workbook = utils.book_new()
    utils.book_append_sheet(workbook, sheet, 'Usage')
    const decoded = read(write(workbook, { bookType: 'xlsx', type: 'array' }), { type: 'array' })

    expect(utils.sheet_to_json(decoded.Sheets.Usage!, { header: 1 })).toEqual([values])
    for (const address of ['A1', 'B1', 'C1', 'D1']) {
      expect(decoded.Sheets.Usage![address].t).toBe('s')
      expect(decoded.Sheets.Usage![address].f).toBeUndefined()
    }
  })
})
