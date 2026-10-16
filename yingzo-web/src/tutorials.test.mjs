import test from 'node:test'
import assert from 'node:assert/strict'
import { isTutorialURL, tutorialArticles, selectedTutorial } from './tutorials.js'

test('reader only uses published Feishu document links', () => {
  const config = { enabled: true, categories: [
    { id: 'start', enabled: true, articles: [
      { id: 'one', enabled: true, url: 'https://my.feishu.cn/docx/abc?from=copy#heading' },
      { id: 'draft', enabled: false, url: 'https://my.feishu.cn/docx/draft' },
      { id: 'unsafe', enabled: true, url: 'javascript:alert(1)' },
    ] },
    { id: 'hidden', enabled: false, articles: [{ id: 'private', enabled: true, url: 'https://my.feishu.cn/docx/private' }] },
  ] }
  assert.deepEqual(tutorialArticles(config).map(a => a.id), ['one'])
  assert.deepEqual(tutorialArticles({ ...config, enabled: false }), [])
  assert.deepEqual(tutorialArticles(null), [])
})

test('deep links restore the selected tutorial and stale links fall back', () => {
  const articles = [{ id: 'one' }, { id: 'two' }]
  assert.equal(selectedTutorial(articles, '?article=two'), articles[1])
  assert.equal(selectedTutorial(articles, '?article=deleted'), articles[0])
  assert.equal(selectedTutorial(articles, ''), articles[0])
  assert.equal(selectedTutorial([], '?article=two'), null)
})

test('document link validation rejects lookalike domains, unsafe schemes and credentials', () => {
  for (const raw of ['https://my.feishu.cn/docx/abc', 'https://team.feishu.cn/wiki/abc', 'https://team.larksuite.com/docs/abc']) assert.equal(isTutorialURL(raw), true)
  for (const raw of ['javascript:alert(1)', 'http://my.feishu.cn/docx/abc', 'https://feishu.cn.evil.com/docx/abc', 'https://my.feishu.cn:444/docx/abc', 'https://user@my.feishu.cn/docx/abc', 'https://my.feishu.cn/redirect']) assert.equal(isTutorialURL(raw), false)
})
