// Copyright © 2019 Oxford Nanopore Technologies.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package cmd

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/biogo/biogo/align"
	"github.com/biogo/biogo/alphabet"
	"github.com/biogo/biogo/feat"
	"github.com/biogo/biogo/seq/linear"
	"github.com/shenwei356/bio/seqio/fastx"
)

// Query holds information about a query sequence.
type Query struct {
	Name      string
	Seq       string
	Strand    string
	NullScore float64
}

// Range defines a  half-open slice over a sequence [Start, End).
type Range struct {
	Start float64
	End   float64
}

// Range returns the length of a range.
func (r Range) Len() float64 {
	return r.End - r.Start
}

// Ranges is a slice of ranges.
type Ranges []Range

// Reference holds information about a reference sequence along with the target ranges.
type Reference struct {
	Name   string
	Seq    string
	Ranges Ranges
}

// Queries is a slice of pointers to Query.
type Queries []*Query

// SeqDetector holds paramters for sequence detection.
type SeqDetector struct {
	Queries   Queries
	SearchAll bool
	Stranded  bool
	NullMode  string
	Cutoff    float64
	AlnParams *AlnParams
}

// NewSeqDetector initilizes a SeqDetector object.
func NewSeqDetector(searchAll bool, stranded bool, nullMode string, cutoff float64, alnParams *AlnParams) *SeqDetector {
	return &SeqDetector{Queries{}, searchAll, stranded, nullMode, cutoff, alnParams}
}

// Detect performs an optinally recursive alignments of the queries of a given reference sequence.
func (d *SeqDetector) Detect(r *Reference, rec bool) []*AlignedSeq {
	var h []*AlignedSeq
	for _, rr := range r.Ranges {
		if rec {
			h = append(h, d.detectRec(r, rr)...)
		} else {
			h = append(h, d.detectOnce(r, rr)...)
		}
	}
	return h
}

// actualRange applies a range to a sequence with a given length.
func actualRange(rr Range, l int) Range {
	s, e := rr.Start, rr.End
	if s == e {
		return rr
	}
	if math.IsNaN(rr.Start) {
		s = 0
	} else if rr.Start < 0 {
		s = float64(l) + rr.Start
	}
	if s < 0 {
		s = 0
	}
	if math.IsNaN(rr.End) {
		e = float64(l)
	} else if rr.End < 0 {
		e = float64(l) + rr.End
	}
	if e > float64(l) {
		e = float64(l)
	}
	if s > e {
		s, e = 0, float64(l)
	}

	return Range{s, e}
}

// detectOnce aligns queries to the reference sequence at specified ranges.
func (d *SeqDetector) detectOnce(r *Reference, rr Range) []*AlignedSeq {
	var hits []*AlignedSeq
	if rr.Len() == 0 {
		return hits
	}
	for _, q := range d.Queries {
		nr := &Reference{r.Name, r.Seq, Ranges{actualRange(rr, len(r.Seq))}}
		h := PairwiseAlignSW(nr, q, d.AlnParams)
		h.Detector = d
		if (h.Score / q.NullScore) > d.Cutoff {
			hits = append(hits, h)
		}
	}
	return bestHits(hits, -1)
}

// detectRec aligns queries to the reference sequence ranges in a recursive fashion in order
// to return all matches above the threshold.
func (d *SeqDetector) detectRec(r *Reference, rr Range) []*AlignedSeq {
	var hits []*AlignedSeq
	if rr.Len() == 0 {
		return hits
	}
	for _, q := range d.Queries {
		nr := &Reference{r.Name, r.Seq, Ranges{actualRange(rr, len(r.Seq))}}
		h := PairwiseAlignSW(nr, q, d.AlnParams)
		h.Detector = d
		if (h.Score / q.NullScore) > d.Cutoff {
			hits = append(hits, h)
		}
	}
	if len(hits) > 0 {
		bh := bestHits(hits, 1)
		bh = append(bh, d.detectRec(r, Range{rr.Start, float64(bh[0].RefStart)})...)
		bh = append(bh, d.detectRec(r, Range{float64(bh[0].RefEnd), rr.End})...)
		hits = bh
	}
	return bestHits(hits, -1)
}

// byScore is a utility type for sorting []*AlignedSeq.
type byScore []*AlignedSeq

func (a byScore) Len() int           { return len(a) }
func (a byScore) Less(i, j int) bool { return a[i].Score > a[j].Score }
func (a byScore) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }

func bestHits(h []*AlignedSeq, n int) []*AlignedSeq {
	if len(h) == 0 {
		return h
	}
	if n < 0 {
		n = len(h)
	}
	res := make([]*AlignedSeq, len(h))
	copy(res, h)
	sort.Stable(byScore(res))
	i := n
	if i > len(res) {
		i = len(res)
	}
	res[0].Best = true
	return res[:i]
}

// LoadQueries loads queries from a fasta file and calculates null scores for each.
func (d *SeqDetector) LoadQueries(fx string) {
	var record *fastx.Record
	var fastxReader *fastx.Reader
	var err error
	if len(d.Queries) == 0 {
		d.Queries = make(Queries, 0, 10)
	}

	fastxReader, err = fastx.NewReader(nil, fx, "")
	checkError(err)

	for {
		record, err = fastxReader.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			checkError(err)
			break
		}

		seq := string(record.Seq.Seq)
		name := strings.Split(string(record.Name), " ")[0]
		ns := d.nullScore(seq)
		d.Queries = append(d.Queries, &Query{name, seq, "+", ns})
		if !d.Stranded {
			d.Queries = append(d.Queries, &Query{name, RevCompDNA(seq), "-", ns})
		}

	}
}

// AddAnonQueries adds anonymous queries from a list of comma separated strings.
func (d *SeqDetector) AddAnonQueries(qrs []string) {
	for i, q := range qrs {
		name := fmt.Sprintf("q%d", i)
		ns := d.nullScore(q)
		d.Queries = append(d.Queries, &Query{name, q, "+", ns})
		if !d.Stranded {
			d.Queries = append(d.Queries, &Query{name, RevCompDNA(q), "-", ns})
		}
	}

}

// nullScore calculates null score for a given query. Currently uses self-alignment.
func (d *SeqDetector) nullScore(q string) float64 {
	switch d.NullMode {
	case "self":
		return PairwiseAlignSW(&Reference{Name: "Ref", Seq: q, Ranges: Ranges{Range{0, float64(len(q))}}}, &Query{Name: "Query", Seq: q}, d.AlnParams).Score
	}
	return math.NaN()
}

// Scorer is an interface for getting alignment score.
type Scorer interface {
	Score() int
}

// NewAnonLinearSeq makes  a new anonymous linear.Seq.
func NewAnonLinearSeq(s string) *linear.Seq {
	return &linear.Seq{Seq: alphabet.BytesToLetters([]byte(s))}
}

// PairwiseAlignSW performs pairwise local alignment of two sequences using the biogo implementation of the Smith-Waterman algorithm.
func PairwiseAlignSW(r *Reference, q *Query, alnParams *AlnParams) *AlignedSeq {
	ref := NewAnonLinearSeq(r.Seq[int(r.Ranges[0].Start):int(r.Ranges[0].End)])
	ref.Alpha = alphabet.DNAgapped
	query := NewAnonLinearSeq(q.Seq)
	query.Alpha = alphabet.DNAgapped

	m := alnParams.Match
	s := alnParams.Mismatch
	o := alnParams.GapOpen
	e := alnParams.GapExtend

	smith := align.SWAffine{
		Matrix: align.Linear{
			{0, e, e, e, e},
			{e, m, s, s, s},
			{e, s, m, s, s},
			{e, s, s, m, s},
			{e, s, s, s, m},
		},
		GapOpen: o,
	}

	aln, err := smith.Align(ref, query)
	var res *AlignedSeq
	if err == nil {
		fa := align.Format(ref, query, aln, '-')
		res = AlignInfo(r, q, aln)
		res.RefAln = fmt.Sprintf("%s", fa[0])
		res.QueryAln = fmt.Sprintf("%s", fa[1])
	} else {
		panic(fmt.Sprintf("Could not align sequences: %s", err))
	}
	return res
}

// AlignedSeq holds alignment results.
type AlignedSeq struct {
	Ref        *Reference
	Query      *Query
	QueryAln   string
	RefAln     string
	RefStart   int
	RefEnd     int
	QueryStart int
	QueryEnd   int
	Score      float64
	Best       bool
	Detector   *SeqDetector
}

// Fields returns the fields of AlignedSeq in a defined order.
func (a *AlignedSeq) Fields() []string {
	validFields := []string{"Ref", "RefStart", "RefEnd", "Query", "QueryStart", "QueryEnd", "Strand", "MapQual", "RawScore", "Acc", "ClipAcc", "QueryCov"}
	return validFields
}

// String generates string represenattion of a *AlignedSeq.
func (a *AlignedSeq) String() string {
	validFields := []string{"Ref", "RefStart", "RefEnd", "Query", "QueryStart", "QueryEnd", "Strand", "MapQual", "RawScore", "Acc", "ClipAcc", "QueryCov"}
	fmap := make(map[string]func(*AlignedSeq) string)
	fmap["Ref"] = func(a *AlignedSeq) string {
		return a.Ref.Name
	}
	fmap["RefStart"] = func(a *AlignedSeq) string {
		return strconv.Itoa(a.RefStart)
	}
	fmap["RefEnd"] = func(a *AlignedSeq) string {
		return strconv.Itoa(a.RefEnd)
	}
	fmap["Query"] = func(a *AlignedSeq) string {
		return a.Query.Name
	}
	fmap["QueryStart"] = func(a *AlignedSeq) string {
		return strconv.Itoa(a.QueryStart)
	}
	fmap["QueryEnd"] = func(a *AlignedSeq) string {
		return strconv.Itoa(a.QueryEnd)
	}
	fmap["Strand"] = func(a *AlignedSeq) string {
		return a.Query.Strand
	}
	fmap["RawScore"] = func(a *AlignedSeq) string {
		return fmt.Sprintf("%.0f", a.Score)
	}
	fmap["MapQual"] = func(a *AlignedSeq) string {
		mq := -10 * math.Log10(1-a.Score/a.Query.NullScore)
		if math.IsInf(mq, 1) {
			mq = 60
		}
		return fmt.Sprintf("%.2f", mq)
	}
	fmap["Acc"] = func(a *AlignedSeq) string {
		diff := 0
		for i, rb := range []byte(a.RefAln) {
			if rb != a.QueryAln[i] {
				diff++
			}
		}
		length := float64(len(a.RefAln))
		acc := (length - float64(diff)) * 100 / length
		return fmt.Sprintf("%.2f", acc)
	}
	fmap["ClipAcc"] = func(a *AlignedSeq) string {
		diff := 0
		for i, rb := range []byte(a.RefAln) {
			if rb != a.QueryAln[i] {
				diff++
			}
		}
		length := float64(len(a.RefAln))
		acc := ((length - float64(diff)) * 100) / (length + float64(len(a.Query.Seq)-a.QueryEnd+a.QueryStart))
		return fmt.Sprintf("%.2f", acc)
	}
	fmap["QueryCov"] = func(a *AlignedSeq) string {
		acc := float64(a.QueryEnd-a.QueryStart) * 100 / float64(len(a.Query.Seq))
		return fmt.Sprintf("%.2f", acc)
	}

	tmp := make([]string, len(validFields))
	for i, f := range validFields {
		tmp[i] = fmap[f](a)
	}

	return strings.Join(tmp, "\t")
}

func (a *AlignedSeq) AlnString() string {
	return fmt.Sprintf("@\t%s\t+\t%d\t%d\t%s\n@\t%s\t%s\t%d\t%d\t%s", a.RefAln, a.RefStart, a.RefEnd, a.Ref.Name, a.QueryAln, a.Query.Strand, a.QueryStart, a.QueryEnd, a.Query.Name)
}

// AlignInfo constructs an *AlignedSeq structure based on raw alignment results.
func AlignInfo(r *Reference, q *Query, f []feat.Pair) *AlignedSeq {
	ref_starts := make([]int, 0)
	ref_ends := make([]int, 0)
	query_starts := make([]int, 0)
	query_ends := make([]int, 0)
	scores := make([]int, 0)

	for _, fs := range f {
		fc := fs.Features()
		fsScorer, _ := fs.(Scorer)
		scores = append(scores, fsScorer.Score())
		ref_starts = append(ref_starts, fc[0].Start())
		ref_ends = append(ref_ends, fc[0].End())
		query_starts = append(query_starts, fc[1].Start())
		query_ends = append(query_ends, fc[1].End())

	}
	res := &AlignedSeq{Ref: r, Query: q}
	res.RefStart = MinInts(ref_starts) + int(r.Ranges[0].Start)
	res.RefEnd = MaxInts(ref_ends) + int(r.Ranges[0].Start)
	res.QueryStart = MinInts(query_starts)
	res.QueryEnd = MaxInts(query_ends)
	res.Score = float64(SumInts(scores))
	return res
}
