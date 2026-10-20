<template>
  <a class="release-banner" href="https://var0.xyz/posts/introducing-syrinx.html">
    Introducing Syrinx: read the announcement →
  </a>
</template>

<style>
:root {
  --vp-layout-top-height: 40px;
}

.release-banner {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  z-index: var(--vp-z-index-layout-top);
  display: flex;
  align-items: center;
  justify-content: center;
  height: var(--vp-layout-top-height);
  padding: 0 16px;
  background: var(--vp-c-brand-1);
  color: var(--vp-c-white);
  font-size: 14px;
  font-weight: 500;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  text-decoration: none;
}

.release-banner:hover {
  background: var(--vp-c-brand-2);
}
</style>
