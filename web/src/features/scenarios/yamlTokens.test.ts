import { describe, expect, it } from 'vitest';
import { checkoutStress } from '@/mocks/yamls';
import { tokenizeLine, type Token } from './yamlTokens';

const kinds = (ts: Token[]) => ts.map((t) => [t.kind, t.text]);

describe('tokenizeLine', () => {
  it('colours keys, values and comments', () => {
    expect(kinds(tokenizeLine('  name: shop # the name'))).toEqual([
      ['plain', '  '],
      ['key', 'name'],
      ['punct', ':'],
      ['plain', ' '],
      ['string', 'shop'],
      ['plain', ' '],
      ['comment', '# the name'],
    ]);
    expect(kinds(tokenizeLine('weight: 9'))).toContainEqual(['number', '9']);
    expect(kinds(tokenizeLine('enabled: true'))).toContainEqual(['keyword', 'true']);
  });

  it('marks list dashes and interpolations', () => {
    const ts = tokenizeLine('      - get: /api/products/${productId}');
    expect(kinds(ts)).toEqual([
      ['plain', '      '],
      ['punct', '-'],
      ['plain', ' '],
      ['key', 'get'],
      ['punct', ':'],
      ['plain', ' '],
      ['string', '/api/products/'],
      ['interp', '${productId}'],
    ]);
  });

  it('keeps a # inside quotes and URLs as text', () => {
    const ts = tokenizeLine('  url: "/a#b" # note');
    expect(ts.find((t) => t.kind === 'comment')?.text).toBe('# note');
    expect(tokenizeLine('get: /a#b').some((t) => t.kind === 'comment')).toBe(false);
  });

  it('never loses or changes text', () => {
    for (const line of checkoutStress.split('\n')) {
      expect(
        tokenizeLine(line)
          .map((t) => t.text)
          .join(''),
      ).toBe(line);
    }
  });
});
