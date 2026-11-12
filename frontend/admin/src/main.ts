import { createApp } from "vue";
import { createPinia } from "pinia";
import { createRouter, createWebHistory } from "vue-router";
import App from "./App.vue";
import "./style.css";
import { useAuth } from "./stores/auth";
const pinia = createPinia();
const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: "/manage/support",
      alias: ["/panel/support", "/admin/support"],
      component: () => import("./views/Support.vue"),
    },
    {
      path: "/manage/student-life",
      alias: ["/panel/student-life", "/admin/student-life"],
      component: () => import("./views/StudentLife.vue"),
    },
    {
      path: "/manage/leisure",
      alias: ["/panel/leisure", "/admin/leisure"],
      component: () => import("./views/Leisure.vue"),
    },
    { path: "/", component: () => import("./views/Dashboard.vue") },
    {
      path: "/manage/studio",
      alias: ["/panel/studio", "/admin/studio"],
      component: () => import("./views/Studio.vue"),
    },
    {
      path: "/manage/catalog",
      alias: ["/panel/catalog", "/admin/catalog"],
      component: () => import("./views/Catalog.vue"),
    },
    {
      path: "/manage",
      alias: ["/panel", "/admin"],
      component: () => import("./views/Manage.vue"),
    },
    {
      path: "/login",
      alias: ["/panel/login", "/admin/login"],
      component: () => import("./views/Login.vue"),
    },
  ].map((route) => ({
    ...route,
    meta: { requiresAuth: route.path.startsWith("/manage") },
  })),
});
router.beforeEach(async (to) => {
  if (!to.meta.requiresAuth) return;
  const auth = useAuth(pinia);
  try {
    await auth.load();
  } catch {
    return "/login";
  }
  if (!auth.actor) return "/login";
});
createApp(App).use(pinia).use(router).mount("#app");
