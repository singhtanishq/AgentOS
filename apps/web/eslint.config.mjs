import next from "eslint-config-next";

// eslint-config-next v16 ships its flat config (next, next/typescript,
// core-web-vitals) as the default export.
const eslintConfig = [
  ...next,
  {
    ignores: ["node_modules/**", ".next/**", "out/**", "coverage/**"],
  },
];

export default eslintConfig;
