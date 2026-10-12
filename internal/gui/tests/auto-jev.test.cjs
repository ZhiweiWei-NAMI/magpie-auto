const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const {test} = require('node:test');
const {chromium,webkit} = require('playwright');
const assets=path.resolve(__dirname,'../assets');

for (const engine of process.env.BROWSER ? [process.env.BROWSER] : ['chromium','webkit']) {
  test(`${engine}: Auto saves a Console key path and inference route in every language`,async()=>{
    const browser=await (engine==='webkit'?webkit:chromium).launch();
    try {
      for (const lang of ['en','zh','zh-TW','ja','de']) {
        const context=await browser.newContext({viewport:{width:1100,height:900}});
        const page=await context.newPage(),errors=[],posts=[];
        page.on('pageerror',error=>errors.push(error.message));
        const settings={lang,theme:'light'};
        const snapshot={configured:true,tasks:[],config:{enabled:true,decider:'jev-1.13-free',
          jevKeyFile:'/private/console.key',maxConcurrent:1,contextBytes:16000,
          routes:[{id:'execute',label:'DeepSeek',adapter:'gateway',model:'dsh-workbuddy/deepseek-v4.1-flash',description:'Inference',efforts:[]}]}};
        await page.route('**/*',async route=>{
          const url=new URL(route.request().url());
          if (url.pathname==='/boot.js') return route.fulfill({contentType:'text/javascript',body:`window.bootPrefs=${JSON.stringify({lang,theme:'light',web:true})};`});
          if (url.pathname==='/api/state') return route.fulfill({json:{agents:[],profiles:[],settings}});
          if (url.pathname==='/api/settings') return route.fulfill({json:settings});
          if (url.pathname==='/api/auto/config') {posts.push(route.request().postDataJSON());return route.fulfill({json:snapshot});}
          if (url.pathname==='/api/auto') return route.fulfill({json:snapshot});
          if (url.pathname==='/api/gateway/trace') return route.fulfill({json:{mine:true,now:new Date().toISOString(),seq:0,totals:{},routes:[]}});
          if (url.pathname==='/api/providers') return route.fulfill({json:{providers:[],presets:[],excluded:[],gateway:{running:true,window:true}}});
          if (url.pathname==='/api/plugins') return route.fulfill({json:{plugins:[]}});
          if (url.pathname.startsWith('/api/')) return route.fulfill({json:{}});
          const file=path.join(assets,url.pathname==='/'?'index.html':url.pathname);
          try {return route.fulfill({body:await fs.readFile(file),contentType:{'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml'}[path.extname(file)]});}
          catch {return route.fulfill({status:404,body:''});}
        });
        await page.goto('http://magpie.test/?view=auto');
        await page.waitForFunction(()=>document.querySelector('#view-auto .auto-head'));
        await page.locator('#view-auto .auto-head button').click();
        await page.waitForFunction(()=>document.querySelector('.auto-config-fields input[type=text]').value==='jev-1.13-free');
        const texts=page.locator('.auto-config-fields input[type=text]');
        assert.equal(await texts.nth(1).inputValue(),'/private/console.key',lang);
        await texts.nth(1).fill('/private/new-console.key');
        await page.locator('.auto-config > button.primary').click();
        await page.waitForFunction(()=>document.querySelector('.auto-config').hidden);
        assert.equal(posts.length,1,lang);
        assert.equal(posts[0].decider,'jev-1.13-free',lang);
        assert.equal(posts[0].jevKeyFile,'/private/new-console.key',lang);
        assert.equal(posts[0].routes[0].adapter,'gateway',lang);
        assert.ok(!('workspace' in posts[0].routes[0])&&!('allowWrites' in posts[0].routes[0]),lang);
        assert.deepEqual(errors,[],lang);
        await context.close();
      }
    } finally {await browser.close();}
  });
}
