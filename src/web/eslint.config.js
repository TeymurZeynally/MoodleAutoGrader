// Static analysis of the web UI: make lint from the repository root (Docker), or npm run lint here.
// Only the rules that find mistakes and keep one style. No formatter and no max-len: long lines are the project's style,
// nothing asks to put each argument or prop on its own line.
import js from '@eslint/js'
import stylistic from '@stylistic/eslint-plugin'
import reactHooks from 'eslint-plugin-react-hooks'
import globals from 'globals'
import tseslint from 'typescript-eslint'

// Statements of the main logic: the body of a function or the file itself, not code nested in another block or a case.
const mainLogic = ':matches(Program, :function > BlockStatement) > *'
const hookCall = 'CallExpression:matches([callee.name=/^use[A-Z]/], [callee.property.name=/^use[A-Z]/])'
const hook = `:function > BlockStatement > :matches(VariableDeclaration:has(VariableDeclarator > ${hookCall}), ExpressionStatement:has(> ${hookCall}))`

export default tseslint.config(
  { ignores: ['dist', 'node_modules'] },
  js.configs.recommended,
  ...tseslint.configs.recommendedTypeChecked,
  ...tseslint.configs.stylisticTypeChecked,
  reactHooks.configs.flat.recommended,
  {
    files: ['**/*.{ts,tsx}'],
    languageOptions: { globals: globals.browser, parserOptions: { projectService: { allowDefaultProject: ['vite.config.ts'] }, tsconfigRootDir: import.meta.dirname } },
    plugins: { '@stylistic': stylistic },
    rules: {
      // React handlers may be async: each one catches its own errors and shows them.
      '@typescript-eslint/no-misused-promises': ['error', { checksVoidReturn: { attributes: false } }],
      // () => {} says "ignore": a failed poll simply tries again.
      '@typescript-eslint/no-empty-function': ['error', { allow: ['arrowFunctions'] }],
      // A || fallback on purpose: an empty string or false also takes the fallback.
      '@typescript-eslint/prefer-nullish-coalescing': ['error', { ignorePrimitives: { string: true, boolean: true } }],
      // const { [id]: _, ...rest } = edits is how a key is removed from an object.
      '@typescript-eslint/no-unused-vars': ['error', { ignoreRestSiblings: true }],
      // The pages reset their state when a dialog opens and then load data, in one effect. The rule wants a data library
      // or keyed components instead: a rewrite of every page for no change in behaviour.
      'react-hooks/set-state-in-effect': 'off',
      '@stylistic/semi': ['error', 'never'],
      '@stylistic/quotes': ['error', 'single', { avoidEscape: true, allowTemplateLiterals: 'always' }],
      '@stylistic/jsx-quotes': ['error', 'prefer-double'],
      '@stylistic/no-trailing-spaces': 'error',
      '@stylistic/eol-last': 'error',
      '@stylistic/no-multiple-empty-lines': ['error', { max: 1 }],
      '@stylistic/comma-spacing': 'error',
      '@stylistic/keyword-spacing': 'error',
      '@stylistic/space-infix-ops': 'error',
      '@stylistic/arrow-spacing': 'error',
      '@stylistic/no-multi-spaces': 'error',
      '@stylistic/no-mixed-spaces-and-tabs': 'error',
      '@stylistic/no-tabs': 'error',
      '@stylistic/indent': ['error', 2, { SwitchCase: 1, flatTernaryExpressions: true }],
      // A blank line after a multi-line statement of the main logic and after a group of hooks; one-liners stay compact.
      // The last matching entry wins, so hooks may follow each other and a multi-line hook is still separated.
      '@stylistic/padding-line-between-statements': ['error',
        { blankLine: 'always', prev: { selector: hook }, next: '*' },
        { blankLine: 'any', prev: { selector: hook }, next: { selector: hook } },
        { blankLine: 'always', prev: { selector: mainLogic, lineMode: 'multiline' }, next: '*' },
      ],
    },
  },
  { files: ['eslint.config.js'], ...tseslint.configs.disableTypeChecked },
)
