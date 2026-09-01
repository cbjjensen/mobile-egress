import { createElement } from 'react'

export function BrandIdentity({ eyebrow, name }) {
  return createElement(
    'div',
    { className: 'brand-identity' },
    createElement('img', {
      className: 'brand-logo',
      src: '/zfnf-logo.png',
      alt: '',
      width: 56,
      height: 56,
    }),
    createElement(
      'div',
      null,
      createElement('p', { className: 'eyebrow' }, eyebrow),
      createElement('h1', null, name),
    ),
  )
}
