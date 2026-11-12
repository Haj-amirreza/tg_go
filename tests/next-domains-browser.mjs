import assert from 'node:assert/strict';
import fs from 'node:fs';
import {createHmac} from 'node:crypto';
import {createRequire} from 'node:module';
const require=createRequire(import.meta.url),{chromium}=require(process.env.TG_PLAYWRIGHT_MODULE||'playwright');
const fixture=JSON.parse(fs.readFileSync(process.env.NEXT_BROWSER_FIXTURE||'/tmp/identity-browser-fixture.json','utf8'));
assert(fixture.database.endsWith('_test'));
const base=process.env.NEXT_BASE_URL||'http://127.0.0.1:8082';
const runId=String(Date.now()).slice(-8),professorTitle='استاد مرورگر '+runId,bookTitle='کتاب رایانه مرورگر '+runId;
function code(secret){const alphabet='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';let bits='';for(const c of secret)bits+=alphabet.indexOf(c).toString(2).padStart(5,'0');const key=Buffer.from(bits.match(/.{8}/g).map(b=>parseInt(b,2)));const counter=Buffer.alloc(8);counter.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));const sum=createHmac('sha1',key).update(counter).digest(),offset=sum[19]&15;return String((sum.readUInt32BE(offset)&0x7fffffff)%1000000).padStart(6,'0');}
const browser=await chromium.launch({headless:true,executablePath:process.env.TG_CHROME||undefined,args:['--no-sandbox']});let count=0;const pass=s=>{count++;console.log('PASS '+s)};
try{
 const context=await browser.newContext({viewport:{width:1440,height:1000}}),page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(base+'/login');await page.getByRole('button',{name:'ورود به پنل'}).waitFor();await page.getByLabel('نام کاربری',{exact:true}).fill(fixture.username);await page.getByLabel('رمز عبور',{exact:true}).fill(fixture.password);await page.locator('#otp').fill(code(fixture.totp_secret));await page.getByRole('button',{name:'ورود به پنل'}).click();await page.waitForURL('**/manage');
 await page.goto(base+'/manage/catalog');await page.getByRole('button',{name:'افزودن رکورد',exact:true}).click();
 await page.getByLabel('نام و نام خانوادگی / عنوان',{exact:true}).fill(professorTitle);await page.getByLabel('دروس، دانشکده، رشته و توضیحات').fill('دروس: ریاضی\nدانشکده: مهندسی\nرشته: برق');await page.getByLabel('شماره تماس',{exact:true}).fill('۰۹۱۲۳۴۵۶۷۸۹');await page.getByLabel('آیدی تلگرام',{exact:true}).fill('@browser_teacher');await page.getByLabel('ایمیل',{exact:true}).fill('teacher@example.test');await page.getByLabel('وضعیت بررسی').selectOption('published');
 const before=await(await page.request.get(base+'/api/v1/admin/catalog?kind=professors')).json();
 await page.getByRole('region',{name:'پیش‌نمایش پیام'}).getByText(professorTitle,{exact:true}).waitFor();assert.deepEqual(await(await page.request.get(base+'/api/v1/admin/catalog?kind=professors')).json(),before);
 await page.getByRole('button',{name:'ثبت تغییرات',exact:true}).click();await page.getByText('تغییرات ثبت شد.',{exact:true}).waitFor();await page.locator('.catalog-list').getByText(professorTitle,{exact:false}).waitFor();
 pass('professor editor, safe preview and actual publication commit');
 await page.getByLabel('بخش',{exact:true}).selectOption('books');await page.getByRole('button',{name:'افزودن کتاب',exact:true}).waitFor();await page.getByRole('button',{name:'صفحه بعد',exact:true}).click();await page.getByText('صفحه 2 از 3',{exact:true}).waitFor();
 await page.getByRole('button',{name:'افزودن کتاب',exact:true}).click();await page.getByLabel('عنوان کتاب',{exact:true}).fill(bookTitle);await page.getByLabel('دسته‌بندی',{exact:true}).selectOption('engineering');await page.getByRole('textbox',{name:'مقدار مشخصه',exact:true}).first().fill('نویسنده آزمایشی');await page.getByRole('button',{name:'ثبت تغییرات',exact:true}).click();await page.getByText('تغییرات ثبت شد.',{exact:true}).waitFor();
 await page.getByRole('textbox',{name:'جست‌وجوی کتاب',exact:true}).fill(bookTitle.replace('رایانه','رايانه'));await page.getByRole('button',{name:'جست‌وجو',exact:true}).click();await page.locator('.catalog-list').getByText(bookTitle,{exact:true}).waitFor();
 pass('book CRUD, Persian/Arabic search and ten-row pagination');
 await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'ویرایش',exact:true}).click();await page.getByRole('heading',{name:'مشخصات کتاب'}).waitFor();assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+2),false);await page.screenshot({path:'tests/runtime/next-catalog-mobile.png',fullPage:true});await page.getByRole('button',{name:'بستن فرم',exact:true}).click();
 await page.locator('.catalog-list input[type=checkbox]').check();page.once('dialog',d=>d.accept());await page.getByRole('button',{name:'بایگانی انتخاب‌شده‌ها',exact:true}).click();await page.getByText('موارد انتخاب‌شده بایگانی شدند.',{exact:true}).waitFor();await page.getByText('رکوردی در این صفحه نیست.',{exact:true}).waitFor();
 pass('mobile layout and version-checked soft deletion');
 await page.goto(base+'/manage');await page.getByRole('button',{name:'خروج امن',exact:true}).click();await page.waitForURL('**/login');await page.getByLabel('نام کاربری',{exact:true}).fill('limited');await page.getByLabel('رمز عبور',{exact:true}).fill(fixture.password);await page.getByRole('button',{name:'ورود به پنل'}).click();await page.waitForURL('**/manage');await page.goto(base+'/manage/catalog');await page.getByRole('alert').filter({hasText:'اجازهٔ مدیریت این بخش را ندارید.'}).waitFor();assert.equal((await page.request.get(base+'/api/v1/admin/books')).status(),403);
 pass('role-limited actor cannot use catalog forms or bypass the API');
 assert.equal(errors.length,0);console.log('CATALOG BROWSER RESULT: '+count+' checks passed');
}finally{await browser.close()}
