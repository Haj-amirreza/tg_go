<script setup lang="ts">
import { ref, reactive, onMounted, computed, toRaw } from "vue";
import { useRoute, useRouter } from "vue-router";
import { useAuth } from "../stores/auth";
import { domainRequest, commandKey } from "../lib/domain-api";
import type { components } from "../lib/api.generated";
import TelegramPreview from "../components/TelegramPreview.vue";
type Content = components["schemas"]["Content"];
type Book = components["schemas"]["Book"];
const auth = useAuth(),
  route = useRoute(),
  router = useRouter();
const kind = ref(route.query.kind === "books" ? "books" : "professors"),
  page = ref(0),
  query = ref(""),
  category = ref(""),
  pages = ref(1),
  error = ref(""),
  notice = ref(""),
  busy = ref(false),
  editing = ref(false),
  key = ref(commandKey());
const contents = ref<Content[]>([]),
  books = ref<Book[]>([]),
  selected = ref<string[]>([]),
  categories = ref<{ id: string; name: string }[]>([]);
const draft = reactive<Content>({
  id: "0",
  kind: "professors",
  title: "",
  body: "",
  metadata: { person_role: "professor", audience_tag: "" },
  status: "draft",
  version: 0,
  publishedAt: null,
  expiresAt: null,
});
const book = reactive<Book>({
    id: "0",
    title: "",
    details: {},
    category: "general",
    revision: 0,
  }),
  details = ref<{ label: string; value: string }[]>([]);
const isBook = computed(() => kind.value === "books");
let previousBody = "";
function keyFor(value: unknown) {
  const body = JSON.stringify(value);
  if (body !== previousBody) {
    previousBody = body;
    key.value = commandKey();
  }
  return key.value;
}
async function load() {
  if (!auth.actor) return;
  busy.value = true;
  error.value = "";
  selected.value = [];
  try {
    if (isBook.value) {
      const value = await domainRequest<{
        rows: Book[];
        page: number;
        pages: number;
        total: number;
      }>(
        "books?" +
          new URLSearchParams({
            query: query.value,
            category: category.value,
            page: String(page.value),
          }),
      );
      books.value = value.rows;
      pages.value = value.pages;
      page.value = value.page;
    } else {
      contents.value = await domainRequest<Content[]>(
        "catalog?" +
          new URLSearchParams({ kind: kind.value, page: String(page.value) }),
      );
      pages.value = page.value + (contents.value.length === 20 ? 2 : 1);
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : "دریافت فهرست انجام نشد.";
  } finally {
    busy.value = false;
  }
}
function newItem() {
  error.value = "";
  notice.value = "";
  editing.value = true;
  Object.assign(draft, {
    id: "0",
    kind: kind.value,
    title: "",
    body: "",
    metadata: {
      person_role: kind.value === "professors" ? "professor" : "staff",
      audience_tag: "",
    },
    status: "draft",
    version: 0,
    publishedAt: null,
    expiresAt: null,
  });
  Object.assign(book, {
    id: "0",
    title: "",
    details: {},
    category: "general",
    revision: 0,
  });
  details.value = [
    { label: "نویسنده", value: "" },
    { label: "ناشر", value: "" },
    { label: "شماره راهنما", value: "" },
  ];
  previousBody = "";
  key.value = commandKey();
}
function editContent(item: Content) {
  Object.assign(draft, structuredClone(toRaw(item)));
  editing.value = true;
  error.value = "";
  notice.value = "";
  previousBody = "";
  key.value = commandKey();
}
function editBook(item: Book) {
  Object.assign(book, structuredClone(toRaw(item)));
  details.value = Object.entries(item.details).map(([label, value]) => ({
    label,
    value: String(value),
  }));
  editing.value = true;
  error.value = "";
  notice.value = "";
  previousBody = "";
  key.value = commandKey();
}
async function save() {
  if (busy.value) return;
  busy.value = true;
  error.value = "";
  try {
    if (isBook.value) {
      book.details = Object.fromEntries(
        details.value
          .filter((r) => r.label.trim() && r.value.trim())
          .map((r) => [r.label.trim(), r.value.trim()]),
      );
      const result = await domainRequest<Book>(
        "books/save",
        "POST",
        book,
        auth.actor!.csrf,
        keyFor(book),
      );
      Object.assign(book, result);
    } else {
      const result = await domainRequest<Content>(
        "catalog/save",
        "POST",
        draft,
        auth.actor!.csrf,
        keyFor(draft),
      );
      Object.assign(draft, result);
    }
    notice.value = "تغییرات ثبت شد.";
    editing.value = false;
    await load();
  } catch (e) {
    error.value = e instanceof Error ? e.message : "ثبت انجام نشد.";
  } finally {
    busy.value = false;
  }
}
async function archive() {
  if (busy.value || !selected.value.length) return;
  if (!confirm("موارد انتخاب‌شده از فهرست فعال حذف شوند؟")) return;
  busy.value = true;
  error.value = "";
  try {
    const items = isBook.value
      ? books.value
          .filter((v) => selected.value.includes(v.id))
          .map((v) => ({ id: v.id, revision: v.revision }))
      : contents.value
          .filter((v) => selected.value.includes(v.id))
          .map((v) => ({ id: v.id, version: v.version }));
    await domainRequest(
      isBook.value ? "books/archive" : "catalog/archive",
      "POST",
      { items },
      auth.actor!.csrf,
      commandKey(),
    );
    notice.value = "موارد انتخاب‌شده بایگانی شدند.";
    await load();
  } catch (e) {
    error.value = e instanceof Error ? e.message : "بایگانی انجام نشد.";
  } finally {
    busy.value = false;
  }
}
async function changeKind() {
  page.value = 0;
  editing.value = false;
  await router.replace({
    path: "/manage/catalog",
    query: { kind: kind.value },
  });
  await load();
}
onMounted(async () => {
  try {
    await auth.load();
    if (!auth.actor) {
      await router.replace("/login");
      return;
    }
    if (!auth.can("content")) {
      error.value = "اجازهٔ مدیریت این بخش را ندارید.";
      return;
    }
    categories.value = await domainRequest("books/categories");
    await load();
  } catch {
    error.value = "ارتباط با سرویس برقرار نشد.";
  }
});
</script>
<template>
  <div class="page-heading">
    <div>
      <p class="eyebrow">اطلاعات دانشگاه</p>
      <h1>اساتید و کتابخانه</h1>
      <p class="muted">
        افزودن، بررسی و ویرایش؛ داده‌های قبلی با همان شناسه حفظ می‌شوند.
      </p>
    </div>
    <RouterLink class="button secondary" to="/manage">حساب مدیریت</RouterLink>
  </div>
  <p v-if="error" class="error" role="alert">{{ error }}</p>
  <p v-if="notice" role="status">{{ notice }}</p>
  <template v-if="auth.can('content')">
    <section class="panel">
      <div class="catalog-toolbar">
        <label
          >بخش
          <select v-model="kind" aria-label="بخش" @change="changeKind">
            <option value="professors">اساتید</option>
            <option value="contacts">اطلاعات تماس</option>
            <option value="books">کتابخانه دانشگاه</option>
          </select></label
        ><button class="button" :disabled="busy" @click="newItem">
          افزودن {{ isBook ? "کتاب" : "رکورد" }}</button
        ><button
          class="button secondary"
          :disabled="busy || !selected.length"
          @click="archive"
        >
          بایگانی انتخاب‌شده‌ها
        </button>
      </div>
      <form
        v-if="isBook"
        class="catalog-toolbar"
        @submit.prevent="
          page = 0;
          load();
        "
      >
        <input
          v-model="query"
          aria-label="جست‌وجوی کتاب"
          placeholder="نام، نویسنده یا شماره کتاب"
        /><select v-model="category" aria-label="دستهٔ کتاب">
          <option value="">همهٔ دسته‌ها</option>
          <option v-for="c in categories" :key="c.id" :value="c.id">
            {{ c.name }}
          </option></select
        ><button class="button secondary" :disabled="busy">جست‌وجو</button>
      </form>
      <p v-if="busy" role="status">در حال بررسی…</p>
      <ul v-if="isBook" class="catalog-list">
        <li v-for="item in books" :key="item.id">
          <label
            ><input v-model="selected" type="checkbox" :value="item.id" />{{
              item.title
            }}</label
          ><button class="button secondary" @click="editBook(item)">
            ویرایش
          </button>
        </li>
      </ul>
      <ul v-else class="catalog-list">
        <li v-for="item in contents" :key="item.id">
          <label
            ><input v-model="selected" type="checkbox" :value="item.id" />{{
              item.title
            }}
            <small class="muted">{{
              {
                draft: "پیش‌نویس",
                pending: "در انتظار بررسی",
                published: "منتشرشده",
                archived: "بایگانی",
              }[item.status]
            }}</small></label
          ><button class="button secondary" @click="editContent(item)">
            بررسی و ویرایش
          </button>
        </li>
      </ul>
      <p
        v-if="!busy && !(isBook ? books.length : contents.length)"
        class="muted"
      >
        رکوردی در این صفحه نیست.
      </p>
      <div class="catalog-toolbar">
        <button
          class="button secondary"
          :disabled="busy || page === 0"
          @click="
            page--;
            load();
          "
        >
          صفحه قبل</button
        ><span>صفحه {{ page + 1 }} از {{ pages }}</span
        ><button
          class="button secondary"
          :disabled="busy || page + 1 >= pages"
          @click="
            page++;
            load();
          "
        >
          صفحه بعد
        </button>
      </div>
    </section>
    <section v-if="editing" class="panel editor-grid">
      <form class="auth-form" @submit.prevent="save">
        <h2>{{ isBook ? "مشخصات کتاب" : "مشخصات و تأیید محتوا" }}</h2>
        <template v-if="isBook"
          ><label for="book-title">عنوان کتاب</label
          ><input
            id="book-title"
            v-model="book.title"
            required
            maxlength="2000"
          /><label for="book-category">دسته‌بندی</label
          ><select id="book-category" v-model="book.category">
            <option v-for="c in categories" :key="c.id" :value="c.id">
              {{ c.name }}
            </option>
          </select>
          <h3>مشخصات تکمیلی</h3>
          <div v-for="(row, index) in details" :key="index" class="detail-row">
            <input
              v-model="row.label"
              aria-label="نام مشخصه"
              placeholder="نام مشخصه"
            /><input
              v-model="row.value"
              aria-label="مقدار مشخصه"
              placeholder="مقدار"
            /><button
              type="button"
              class="button secondary"
              @click="details.splice(index, 1)"
            >
              حذف
            </button>
          </div>
          <button
            type="button"
            class="button secondary"
            @click="details.push({ label: '', value: '' })"
          >
            افزودن مشخصه
          </button></template
        >
        <template v-else
          ><label for="content-title">نام و نام خانوادگی / عنوان</label
          ><input
            id="content-title"
            v-model="draft.title"
            required
            maxlength="200"
          /><label for="content-body">دروس، دانشکده، رشته و توضیحات</label
          ><textarea
            id="content-body"
            v-model="draft.body"
            rows="6"
            maxlength="30000"
          ></textarea
          ><label for="phone">شماره تماس</label
          ><input
            id="phone"
            v-model="draft.metadata.office_phone"
            dir="ltr"
          /><label for="handle">آیدی تلگرام</label
          ><input
            id="handle"
            v-model="draft.metadata.foreign_id"
            dir="ltr"
          /><label for="email">ایمیل</label
          ><input
            id="email"
            v-model="draft.metadata.email"
            type="email"
            dir="ltr"
          /><label for="status">وضعیت بررسی</label
          ><select id="status" v-model="draft.status">
            <option value="draft">پیش‌نویس</option>
            <option value="pending">در انتظار بررسی</option>
            <option v-if="auth.can('publish')" value="published">
              تأیید و انتشار
            </option>
            <option value="archived">بایگانی</option>
          </select></template
        >
        <button class="button" :disabled="busy">
          {{ busy ? "در حال ثبت…" : "ثبت تغییرات" }}</button
        ><button
          class="button secondary"
          type="button"
          @click="editing = false"
        >
          بستن فرم
        </button>
      </form>
      <TelegramPreview
        :title="isBook ? book.title : draft.title"
        :body="isBook ? '' : draft.body"
        :fields="
          isBook
            ? Object.fromEntries(
                details.filter((r) => r.label).map((r) => [r.label, r.value]),
              )
            : {
                'شماره تماس': draft.metadata.office_phone,
                آیدی: draft.metadata.foreign_id,
                ایمیل: draft.metadata.email,
              }
        "
      />
    </section>
  </template>
</template>
