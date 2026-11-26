import { h, api, pageHead, work, ib, h2s, hint, note, kv, bullets, pill, empty, btn, errText } from '../core.js';
import { markdownHTML, ABOUT_REPO } from '../markdown.js';

function docPage(title, desc, path, emptyText, actions) {
  return async () => {
    let body;
    try {
      const d = await api(path);
      // 空态写成「标题 + 一句为什么 + 去哪看」。
      // 旧后台只丢一句「没有更新日志」：用户看不出是文件真的空、还是没打包进
      // 发布包、还是接口坏了，只能去翻源码。这四种情况的处置完全不同。
      body = d.content
        ? h('div', { class: 'md', html: markdownHTML(d.content) })
        : empty(emptyText, `服务端返回了 ${d.filename ? d.filename + '（内容为空）' : '空内容'}。`
          + '发布包里应该有这个文件；自己从源码直接运行的话，它可能没被带进来，这不影响其它功能。');
    } catch (e) {
      body = empty('读取失败', errText(e) + '。如果是自己从源码运行，没有发布包里的文件是正常的。');
    }
    const aside = [
      ib('这一页是什么', null, bullets([
        '内容来自服务端随包带的文档文件，前端只负责渲染。',
        '从源码直接跑的话，发布包里的文件可能不存在，这时读不到是正常的。',
        '这里不能编辑，改了也不会有用。',
      ])),
      ib('相关', null, h('div', { class: 'pills' },
        h('a', { class: 'pill', href: '#about', text: '关于这个项目' }),
        h('a', { class: 'pill', href: '#release', text: '版本更新' }),
        h('a', { class: 'pill', href: '#audit', text: '审计日志' }))),
    ];
    const inner = [h('div', { class: 'md-wrap' }, body)];
    if (actions) inner.push(h('div', { style: { marginTop: '18px' } }, actions));
    return [pageHead(title, desc), work(inner, aside)];
  };
}

export const changelog = docPage('更新日志', '这个版本新增和修复了什么。', '/api/release-notes', '没有更新日志');
export const about = docPage('关于', '免费开源的 RF4 本地服务器。以下是项目说明。', '/api/about', '没有项目说明',
  h('a', { class: 'btn', href: ABOUT_REPO, target: '_blank', rel: 'noreferrer', text: '在 GitHub 上查看' }));
