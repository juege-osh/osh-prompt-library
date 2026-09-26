(() => {
  const videoColor = '#86e5dd';
  const imageColor = '#e9bb77';
  const fmt = value => Number(value || 0).toLocaleString('zh-CN', {maximumFractionDigits: 2});
  const escape = value => String(value).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  let current = null;

  function destroy() {
    if (!current) return;
    current.controller.abort();
    current.charts.forEach(chart => chart.destroy());
    current = null;
  }

  function mount(root, {api, onQuota}) {
    destroy();
    const view = {controller: new AbortController(), charts: [], request: 0};
    current = view;
    root.innerHTML = `
      <div class="sectionTitle usageHeading"><h2>使用记录</h2><button type="button" class="iconButton usageRefresh" title="刷新使用记录" aria-label="刷新使用记录"><i data-lucide="refresh-cw"></i></button></div>
      <p class="usageStatus" role="status">正在加载使用记录...</p>
      <div class="usageContent" hidden>
        <section class="usageSection" aria-labelledby="distributionTitle">
          <div class="sectionTitle"><h3 id="distributionTitle">购买分布</h3><span>累计购买数量</span></div>
          <div class="usageDistribution">
            <div class="donutWrap"><canvas id="usageDonut" role="img" aria-label="视频和图片提示词购买数量占比" aria-describedby="distributionTable"></canvas><div class="donutCenter"><strong id="purchaseCount">0</strong><span>已购提示词</span></div></div>
            <div class="usageTableWrap"><table class="usageTable" id="distributionTable"><thead><tr><th scope="col">资源类型</th><th scope="col">购买数量</th><th scope="col">占比</th><th scope="col">消耗积分</th></tr></thead><tbody></tbody></table></div>
          </div>
        </section>
        <section class="usageSection" aria-labelledby="trendTitle">
          <div class="sectionTitle"><h3 id="trendTitle">积分使用趋势</h3><span>最近 10 天 · 北京时间</span></div>
          <div class="trendMeta"><div class="chartLegend"><span><i class="usageSwatch video"></i>视频</span><span><i class="usageSwatch image"></i>图片</span></div><span class="trendPeriod"></span></div>
          <p class="trendEmpty" hidden>最近 10 天暂无消费记录</p>
          <div class="trendCanvasWrap"><canvas id="usageTrend" role="img" aria-label="最近十天的视频和图片提示词每日积分消耗" aria-describedby="dailyUsageTable"></canvas></div>
          <table class="srOnly" id="dailyUsageTable"><caption>每日积分消耗，按北京时间统计</caption><thead><tr><th>日期</th><th>视频</th><th>图片</th></tr></thead><tbody></tbody></table>
        </section>
      </div>`;
    const refresh = root.querySelector('.usageRefresh');
    const status = root.querySelector('.usageStatus');
    const content = root.querySelector('.usageContent');
    async function load() {
      const request = ++view.request;
      refresh.disabled = true;
      root.setAttribute('aria-busy', 'true');
      status.textContent = '正在加载使用记录...';
      status.classList.remove('error');
      try {
        const data = await api('/api/user/usage?days=10', {signal: view.controller.signal});
        if (current !== view || request !== view.request || !root.isConnected) return;
        onQuota(data.quota);
        view.charts.forEach(chart => chart.destroy());
        view.charts = [];
        content.hidden = false;
        draw(root, view, data);
        status.textContent = '';
      } catch (err) {
        if (err.name === 'AbortError' || current !== view || !root.isConnected) return;
        if (err.status === 401) {
          view.charts.forEach(chart => chart.destroy());
          view.charts = [];
          content.hidden = true;
        }
        onQuota(null);
        status.textContent = err.status === 401 ? '登录已失效，请重新登录' : '使用记录加载失败，请点击刷新重试';
        status.classList.add('error');
      } finally {
        if (current === view) { refresh.disabled = false; root.setAttribute('aria-busy', 'false'); }
      }
    }
    refresh.addEventListener('click', load);
    window.OshShell?.icons();
    load();
  }

  function draw(root, view, data) {
    const items = ['video','image'].map(kind => data.distribution.find(item => item.kind === kind) || {kind, purchases:0, points:0});
    const total = items.reduce((sum, item) => sum + item.purchases, 0);
    root.querySelector('#purchaseCount').textContent = fmt(total);
    root.querySelector('#distributionTable tbody').innerHTML = items.map(item => `<tr><th scope="row"><span class="usageType"><i class="usageSwatch ${item.kind}"></i>${item.kind==='video'?'视频提示词':'图片提示词'}</span></th><td>${fmt(item.purchases)}</td><td>${total?fmt(item.purchases/total*100):'0'}%</td><td>${fmt(item.points)}</td></tr>`).join('');
    const days = data.daily;
    root.querySelector('.trendPeriod').textContent = `${days[0].date} 至 ${days[days.length-1].date}`;
    root.querySelector('.trendEmpty').hidden = days.some(day=>day.video > 0 || day.image > 0);
    root.querySelector('#dailyUsageTable tbody').innerHTML = days.map(day=>`<tr><th scope="row">${escape(day.date)}</th><td>${fmt(day.video)}</td><td>${fmt(day.image)}</td></tr>`).join('');
    const animation = matchMedia('(prefers-reduced-motion: reduce)').matches ? false : {duration: 300};
    view.charts.push(new Chart(root.querySelector('#usageDonut'), {
      type: 'doughnut',
      data: {labels:total?['视频提示词','图片提示词']:['暂无购买'], datasets:[{
        data: total ? items.map(item=>item.purchases) : [1],
        backgroundColor:total?[videoColor,imageColor]:['#30343a'],
        borderWidth:0, spacing:total && items.every(item=>item.purchases>0)?3:0, hoverOffset:total?3:0
      }]},
      options: {responsive:true, maintainAspectRatio:false, cutout:'72%', animation, layout:{padding:5},
        plugins:{legend:{display:false},tooltip:{enabled:total>0, callbacks:{label:context=>`${context.label}: ${fmt(context.raw)} 条 (${fmt(context.raw/total*100)}%)`}}}
      }
    }));
    view.charts.push(new Chart(root.querySelector('#usageTrend'), {
      type:'line',
      data:{labels:days.map(day=>day.date), datasets:[
        {label:'视频',data:days.map(day=>day.video),borderColor:videoColor,backgroundColor:videoColor},
        {label:'图片',data:days.map(day=>day.image),borderColor:imageColor,backgroundColor:imageColor}
      ].map(dataset=>({...dataset,borderWidth:2,pointRadius:3,pointHoverRadius:5,pointHitRadius:16,tension:.25,cubicInterpolationMode:'monotone',fill:false}))},
      options:{responsive:true, maintainAspectRatio:false, animation, interaction:{mode:'index',intersect:false},
        onResize(chart,size) { chart.options.scales.x.ticks.maxTicksLimit=size.width<600?5:10; },
        plugins:{legend:{display:false},tooltip:{callbacks:{label:context=>`${context.dataset.label}: ${fmt(context.parsed.y)} 积分`}}},
        scales:{
          x:{grid:{display:false},border:{display:false},ticks:{color:'#979ba3',maxRotation:0,maxTicksLimit:root.querySelector('.trendCanvasWrap').clientWidth<600?5:10,font:{size:11},callback(value){return this.getLabelForValue(value).slice(5).replace('-','/');}}},
          y:{beginAtZero:true,suggestedMax:1,border:{display:false},grid:{color:'#2a2e33'},title:{display:true,text:'积分',color:'#979ba3'},ticks:{color:'#979ba3',maxTicksLimit:5,font:{size:11},callback:value=>fmt(value)}}
        }
      }
    }));
  }

  window.addEventListener('osh-user-change', destroy);
  window.addEventListener('pagehide', destroy);
  window.OshUsage = {mount, destroy};
})();
