import React from 'react';

import { render, screen } from 'design/utils/testing';

import { DialogTitleV2 } from 'design/Dialog/DialogTitleV2';

describe('dialogTitleV2', () => {
  // eslint-disable-next-line jest/require-hook
  [
    {
      description: 'all caps',
      props: { title: 'ALL CAPS' },
      expected: 'All Caps',
    },
    {
      description: 'all lower',
      props: { title: 'all lower' },
      expected: 'All Lower',
    },
    {
      description: 'one word',
      props: { title: 'OnE' },
      expected: 'One',
    },
    {
      description: 'two words',
      props: { title: 'tWo WoRdS' },
      expected: 'Two Words',
    },
    {
      description: 'full sentence',
      props: { title: 'The quick Brown fox jumps over tHe lazy dog' },
      expected: 'The Quick Brown Fox Jumps Over The Lazy Dog',
    },
    {
      description: 'full of symbols',
      props: { title: '$tory of a l!fe' },
      expected: '$tory Of A L!fe',
    },
    {
      description: 'contains dots',
      props: { title: 'has.dots' },
      expected: 'Has.dots',
    },
    {
      description: 'multiple sentences',
      props: { title: 'i was. I AM.' },
      expected: 'I Was. I Am.',
    },
    {
      description: 'already title case',
      props: { title: 'This Title Is Good.' },
      expected: 'This Title Is Good.',
    },
  ].forEach(spec => {
    test(`renders title case when title is ${spec.description}`, () => {
      render(<DialogTitleV2 {...spec.props} />);

      expect(screen.getByText(spec.expected)).toBeInTheDocument();
    });
  });
});
