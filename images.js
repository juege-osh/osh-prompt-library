const $ = id => document.getElementById(id);
const state = {page: 1, size: 24, total: 0, q: '', source: ''};
const cache = new Map();
let activeId = '', activeButton = null, requestId = 0, loadId = 0, debounce;
function hidePrompt() {
  requestId++;
  $('promptFloat').hidden = true;
  if (activeButton) activeButton.setAttribute('aria-expanded', 'false');
  activeButton = null;
  activeId = '';
}
function positionPrompt() {
  if (!activeButton || $('promptFloat').hidden) return;
  const panel = $('promptFloat'), anchor = activeButton.getBoundingClientRect();
  const width = Math.min(440, innerWidth - 24);
  panel.style.width = `${width}px`;
  panel.style.left = `${Math.max(12, Math.min(anchor.left, innerWidth - width - 12))}px`;
  panel.style.top = `${anchor.bottom + panel.offsetHeight + 20 < innerHeight ? anchor.bottom + 10 : Math.max(12, anchor.top - panel.offsetHeight - 10)}px`;
}
async function showPrompt(id, button) {
  if (activeButton === button && !$('promptFloat').hidden) { hidePrompt(); return; }
  hidePrompt();
  activeButton = button;
  activeId = id;
  button.setAttribute('aria-expanded', 'true');
  $('promptFloat').hidden = false;
  $('promptText').textContent = '正在加载提示词…';
  $('promptStatus').textContent = '';
  $('copyPrompt').disabled = $('downloadPack').disabled = true;
  $('downloadPack').hidden = true;
  positionPrompt();
  const current = ++requestId;
  try {
    if (!cache.has(id)) {
      const response = await fetch(`/api/images/${encodeURIComponent(id)}`);
      if (!response.ok) throw new Error('无法加载');
      cache.set(id, await response.json());
    }
    if (current !== requestId) return;
    const detail = cache.get(id);
    $('promptText').textContent = detail.prompt_zh || '这张图片的中文提示词正在补齐。';
    $('promptStatus').textContent = detail.prompt_zh ? '图片与完整中文提示词已配对' : '原始提示词已保存，中文译文待补齐';
    $('copyPrompt').disabled = $('downloadPack').disabled = !detail.prompt_zh;
    $('downloadPack').hidden = !detail.image;
  } catch {
    if (current === requestId) $('promptText').textContent = '暂时无法加载，请重试。';
  }
  positionPrompt();
}
function makeCard(item) {
  const card = document.createElement('article');
  card.className = 'card';
  const picture = document.createElement('a');
  picture.className = 'imageButton';
  picture.href = `/image-media/${encodeURIComponent(item.id)}/original`;
  picture.target = '_blank';
  picture.rel = 'noopener noreferrer';
  picture.setAttribute('aria-label', '查看图片原图');
  const image = document.createElement('img');
  image.src = item.thumbnail;
  image.loading = 'lazy';
  image.decoding = 'async';
  image.alt = '生成图片';
  picture.append(image);
  const body = document.createElement('div');
  body.className = 'cardBody';
  const meta = document.createElement('p');
  meta.className = 'meta';
  meta.textContent = [item.model, item.width && item.height ? `${item.width} × ${item.height}` : '图片案例'].filter(Boolean).join(' · ');
  const button = document.createElement('button');
  button.className = 'promptAction';
  button.type = 'button';
  button.textContent = '查看';
  button.setAttribute('aria-label', '查看提示词');
  button.setAttribute('aria-expanded', 'false');
  button.setAttribute('aria-controls', 'promptFloat');
  button.addEventListener('click', () => showPrompt(item.id, button));
  body.append(meta, button);
  card.append(picture, body);
  return card;
}
async function load() {
  const current = ++loadId;
  hidePrompt();
  const params = new URLSearchParams({page: state.page, q: state.q, source: state.source});
  const response = await fetch(`/api/images?${params}`);
  if (!response.ok) throw new Error('无法加载');
  const data = await response.json();
  if (current !== loadId) return;
  state.total = data.total;
  $('allCount').textContent = data.all.toLocaleString();
  $('translatedCount').textContent = `已有中文提示词 ${data.translated.toLocaleString()} 张`;
  $('resultCount').textContent = `找到 ${data.total.toLocaleString()} 张图片`;
  $('grid').replaceChildren(...data.items.map(makeCard));
  if (!data.items.length) $('grid').textContent = '没有找到符合条件的图片';
  $('pageText').textContent = `第 ${state.page} / ${Math.max(1, Math.ceil(data.total / data.size))} 页`;
  $('prev').disabled = state.page <= 1;
  $('next').disabled = state.page * data.size >= data.total;
}
$('search').addEventListener('input', () => {
  clearTimeout(debounce);
  debounce = setTimeout(() => { state.q = $('search').value.trim(); state.page = 1; load().catch(showError); }, 280);
});
$('source').addEventListener('change', () => { state.source = $('source').value; state.page = 1; load().catch(showError); });
$('prev').addEventListener('click', () => { state.page--; load().catch(showError); scrollTo(0, 0); });
$('next').addEventListener('click', () => { state.page++; load().catch(showError); scrollTo(0, 0); });
$('closePrompt').addEventListener('click', hidePrompt);
$('copyPrompt').addEventListener('click', async () => {
  const text = cache.get(activeId)?.prompt_zh;
  if (!text) return;
  await navigator.clipboard.writeText(text);
  $('copyPrompt').textContent = '已复制';
  setTimeout(() => $('copyPrompt').textContent = '复制提示词', 1400);
});
const encoder = new TextEncoder();
const crcTable = Uint32Array.from({length: 256}, (_, n) => {for (let i = 0; i < 8; i++) n = n & 1 ? 0xedb88320 ^ (n >>> 1) : n >>> 1; return n >>> 0;});
function crc32(bytes) {let value = 0xffffffff; for (const byte of bytes) value = crcTable[(value ^ byte) & 255] ^ (value >>> 8); return (value ^ 0xffffffff) >>> 0;}
function zipFiles(files) {
  const chunks = [], records = []; let offset = 0;
  for (const file of files) {
    const name = encoder.encode(file.name), data = file.data, crc = crc32(data), local = new Uint8Array(30 + name.length), l = new DataView(local.buffer);
    l.setUint32(0, 0x04034b50, true); l.setUint16(4, 20, true); l.setUint16(6, 0x0800, true); l.setUint32(14, crc, true); l.setUint32(18, data.length, true); l.setUint32(22, data.length, true); l.setUint16(26, name.length, true); local.set(name, 30);
    chunks.push(local, data); records.push({name, data, crc, offset}); offset += local.length + data.length;
  }
  const directoryOffset = offset;
  for (const record of records) {
    const central = new Uint8Array(46 + record.name.length), c = new DataView(central.buffer);
    c.setUint32(0, 0x02014b50, true); c.setUint16(4, 20, true); c.setUint16(6, 20, true); c.setUint16(8, 0x0800, true); c.setUint32(16, record.crc, true); c.setUint32(20, record.data.length, true); c.setUint32(24, record.data.length, true); c.setUint16(28, record.name.length, true); c.setUint32(42, record.offset, true); central.set(record.name, 46); chunks.push(central); offset += central.length;
  }
  const end = new Uint8Array(22), e = new DataView(end.buffer); e.setUint32(0, 0x06054b50, true); e.setUint16(8, records.length, true); e.setUint16(10, records.length, true); e.setUint32(12, offset - directoryOffset, true); e.setUint32(16, directoryOffset, true); chunks.push(end);
  return new Blob(chunks, {type: 'application/zip'});
}
$('downloadPack').addEventListener('click', async () => {
  const id = activeId, detail = cache.get(id);
  if (!detail?.prompt_zh || !detail.image) return;
  const button = $('downloadPack'); button.disabled = true; button.textContent = '正在打包…';
  try {
    const response = await fetch(detail.image);
    if (!response.ok) throw new Error('图片读取失败');
    const type = response.headers.get('content-type') || '';
    const ext = type.includes('png') ? 'png' : type.includes('webp') ? 'webp' : type.includes('gif') ? 'gif' : 'jpg';
    const blob = zipFiles([{name:'提示词.txt',data:encoder.encode(detail.prompt_zh+'\n')},{name:`原图.${ext}`,data:new Uint8Array(await response.arrayBuffer())}]);
    const url = URL.createObjectURL(blob), link = document.createElement('a'); link.href = url; link.download = `${id}-图片与提示词.zip`; link.click(); setTimeout(() => URL.revokeObjectURL(url), 60000);
  } catch { $('promptStatus').textContent = '打包失败，请稍后重试。'; }
  finally {button.disabled = false; button.textContent = '下载全部资产';}
});
document.addEventListener('pointerdown', event => {if (!$('promptFloat').hidden && !$('promptFloat').contains(event.target) && event.target !== activeButton) hidePrompt();});
document.addEventListener('keydown', event => {if (event.key === 'Escape') hidePrompt();});
window.addEventListener('resize', positionPrompt);
function showError() { $('resultCount').textContent = '暂时无法加载，请刷新重试'; }
load().catch(showError);
