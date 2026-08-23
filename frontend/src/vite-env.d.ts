/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_DUCKLAB_ENGINE?: string;
  readonly VITE_DUCKLAB_TOKEN?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
