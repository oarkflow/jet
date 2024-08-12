// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Multiprecision decimal numbers.
// For floating-point formatting only; not general purpose.
// Only operations are assign and (binary) left/right shift.
// Can do binary floating point in multiprecision decimal precisely
// because 2 divides 10; cannot do decimal floating point
// in multiprecision binary precisely.

package fastprinter

type decimal struct {
	d     [800]byte // digits, big-endian representation
	nd    int       // number of digits used
	dp    int       // decimal point
	neg   bool
	trunc bool // discarded nonzero digits beyond d[:nd]
}

// trim trailing zeros from number.
// (They are meaningless; the decimal point is tracked
// independent of the number of digits.)
func trim(a *decimal) {
	for a.nd > 0 && a.d[a.nd-1] == '0' {
		a.nd--
	}
	if a.nd == 0 {
		a.dp = 0
	}
}

// Assign v to a.
func (a *decimal) Assign(v uint64) {
	var buf [24]byte

	// Write reversed decimal in buf.
	n := 0
	for v > 0 {
		v1 := v / 10
		v -= 10 * v1
		buf[n] = byte(v + '0')
		n++
		v = v1
	}

	// Reverse again to produce forward decimal in a.d.
	a.nd = 0
	for n--; n >= 0; n-- {
		a.d[a.nd] = buf[n]
		a.nd++
	}
	a.dp = a.nd
	trim(a)
}

// Maximum shift that we can do in one pass without overflow.
// A uint has 32 or 64 bits, and we have to be able to accommodate 9<<k.
const uintSize = 32 << (^uint(0) >> 63)
const maxShift = uintSize - 4

// Binary shift right (/ 2) by k bits.  k <= maxShift to avoid overflow.
func rightShift(a *decimal, k uint) {
	r := 0 // read pointer
	w := 0 // write pointer

	// Pick up enough leading digits to cover first shift.
	var n uint
	for ; n>>k == 0; r++ {
		if r >= a.nd {
			if n == 0 {
				// a == 0; shouldn't get here, but handle anyway.
				a.nd = 0
				return
			}
			for n>>k == 0 {
				n = n * 10
				r++
			}
			break
		}
		c := uint(a.d[r])
		n = n*10 + c - '0'
	}
	a.dp -= r - 1

	// Pick up a digit, put down a digit.
	for ; r < a.nd; r++ {
		c := uint(a.d[r])
		dig := n >> k
		n -= dig << k
		a.d[w] = byte(dig + '0')
		w++
		n = n*10 + c - '0'
	}

	// Put down extra digits.
	for n > 0 {
		dig := n >> k
		n -= dig << k
		if w < len(a.d) {
			a.d[w] = byte(dig + '0')
			w++
		} else if dig > 0 {
			a.trunc = true
		}
		n = n * 10
	}

	a.nd = w
	trim(a)
}

// Cheat sheet for left shift: table indexed by shift count giving
// number of new digits that will be introduced by that shift.
//
// For example, leftcheats[4] = {2, "625"}.  That means that
// if we are shifting by 4 (multiplying by 16), it will add 2 digits
// when the string prefix is "625" through "999", and one fewer digit
// if the string prefix is "000" through "624".
//
// Credit for this trick goes to Ken.

type leftCheat struct {
	cutoff string
	delta  int
}

var leftcheats = []leftCheat{
	// Leading digits of 1/2^i = 5^i.
	// 5^23 is not an exact 64-bit floating point number,
	// so have to use bc for the math.
	// Go up to 60 to be large enough for 32bit and 64bit platforms.
	/*
		seq 60 | sed 's/^/5^/' | bc |
		awk 'BEGIN{ print "\t{ 0, \"\" }," }
		{
			log2 = log(2)/log(10)
			printf("\t{ %d, \"%s\" },\t// * %d\n",
				int(log2*NR+1), $0, 2**NR)
		}'
	*/
	{delta: 0, cutoff: ""},
	{delta: 1, cutoff: "5"},                                           // * 2
	{delta: 1, cutoff: "25"},                                          // * 4
	{delta: 1, cutoff: "125"},                                         // * 8
	{delta: 2, cutoff: "625"},                                         // * 16
	{delta: 2, cutoff: "3125"},                                        // * 32
	{delta: 2, cutoff: "15625"},                                       // * 64
	{delta: 3, cutoff: "78125"},                                       // * 128
	{delta: 3, cutoff: "390625"},                                      // * 256
	{delta: 3, cutoff: "1953125"},                                     // * 512
	{delta: 4, cutoff: "9765625"},                                     // * 1024
	{delta: 4, cutoff: "48828125"},                                    // * 2048
	{delta: 4, cutoff: "244140625"},                                   // * 4096
	{delta: 4, cutoff: "1220703125"},                                  // * 8192
	{delta: 5, cutoff: "6103515625"},                                  // * 16384
	{delta: 5, cutoff: "30517578125"},                                 // * 32768
	{delta: 5, cutoff: "152587890625"},                                // * 65536
	{delta: 6, cutoff: "762939453125"},                                // * 131072
	{delta: 6, cutoff: "3814697265625"},                               // * 262144
	{delta: 6, cutoff: "19073486328125"},                              // * 524288
	{delta: 7, cutoff: "95367431640625"},                              // * 1048576
	{delta: 7, cutoff: "476837158203125"},                             // * 2097152
	{delta: 7, cutoff: "2384185791015625"},                            // * 4194304
	{delta: 7, cutoff: "11920928955078125"},                           // * 8388608
	{delta: 8, cutoff: "59604644775390625"},                           // * 16777216
	{delta: 8, cutoff: "298023223876953125"},                          // * 33554432
	{delta: 8, cutoff: "1490116119384765625"},                         // * 67108864
	{delta: 9, cutoff: "7450580596923828125"},                         // * 134217728
	{delta: 9, cutoff: "37252902984619140625"},                        // * 268435456
	{delta: 9, cutoff: "186264514923095703125"},                       // * 536870912
	{delta: 10, cutoff: "931322574615478515625"},                      // * 1073741824
	{delta: 10, cutoff: "4656612873077392578125"},                     // * 2147483648
	{delta: 10, cutoff: "23283064365386962890625"},                    // * 4294967296
	{delta: 10, cutoff: "116415321826934814453125"},                   // * 8589934592
	{delta: 11, cutoff: "582076609134674072265625"},                   // * 17179869184
	{delta: 11, cutoff: "2910383045673370361328125"},                  // * 34359738368
	{delta: 11, cutoff: "14551915228366851806640625"},                 // * 68719476736
	{delta: 12, cutoff: "72759576141834259033203125"},                 // * 137438953472
	{delta: 12, cutoff: "363797880709171295166015625"},                // * 274877906944
	{delta: 12, cutoff: "1818989403545856475830078125"},               // * 549755813888
	{delta: 13, cutoff: "9094947017729282379150390625"},               // * 1099511627776
	{delta: 13, cutoff: "45474735088646411895751953125"},              // * 2199023255552
	{delta: 13, cutoff: "227373675443232059478759765625"},             // * 4398046511104
	{delta: 13, cutoff: "1136868377216160297393798828125"},            // * 8796093022208
	{delta: 14, cutoff: "5684341886080801486968994140625"},            // * 17592186044416
	{delta: 14, cutoff: "28421709430404007434844970703125"},           // * 35184372088832
	{delta: 14, cutoff: "142108547152020037174224853515625"},          // * 70368744177664
	{delta: 15, cutoff: "710542735760100185871124267578125"},          // * 140737488355328
	{delta: 15, cutoff: "3552713678800500929355621337890625"},         // * 281474976710656
	{delta: 15, cutoff: "17763568394002504646778106689453125"},        // * 562949953421312
	{delta: 16, cutoff: "88817841970012523233890533447265625"},        // * 1125899906842624
	{delta: 16, cutoff: "444089209850062616169452667236328125"},       // * 2251799813685248
	{delta: 16, cutoff: "2220446049250313080847263336181640625"},      // * 4503599627370496
	{delta: 16, cutoff: "11102230246251565404236316680908203125"},     // * 9007199254740992
	{delta: 17, cutoff: "55511151231257827021181583404541015625"},     // * 18014398509481984
	{delta: 17, cutoff: "277555756156289135105907917022705078125"},    // * 36028797018963968
	{delta: 17, cutoff: "1387778780781445675529539585113525390625"},   // * 72057594037927936
	{delta: 18, cutoff: "6938893903907228377647697925567626953125"},   // * 144115188075855872
	{delta: 18, cutoff: "34694469519536141888238489627838134765625"},  // * 288230376151711744
	{delta: 18, cutoff: "173472347597680709441192448139190673828125"}, // * 576460752303423488
	{delta: 19, cutoff: "867361737988403547205962240695953369140625"}, // * 1152921504606846976
}

// Is the leading prefix of b lexicographically less than s?
func prefixIsLessThan(b []byte, s string) bool {
	for i := 0; i < len(s); i++ {
		if i >= len(b) {
			return true
		}
		if b[i] != s[i] {
			return b[i] < s[i]
		}
	}
	return false
}

// Binary shift left (* 2) by k bits.  k <= maxShift to avoid overflow.
func leftShift(a *decimal, k uint) {
	delta := leftcheats[k].delta
	if prefixIsLessThan(a.d[0:a.nd], leftcheats[k].cutoff) {
		delta--
	}

	r := a.nd         // read index
	w := a.nd + delta // write index

	// Pick up a digit, put down a digit.
	var n uint
	for r--; r >= 0; r-- {
		n += (uint(a.d[r]) - '0') << k
		quo := n / 10
		rem := n - 10*quo
		w--
		if w < len(a.d) {
			a.d[w] = byte(rem + '0')
		} else if rem != 0 {
			a.trunc = true
		}
		n = quo
	}

	// Put down extra digits.
	for n > 0 {
		quo := n / 10
		rem := n - 10*quo
		w--
		if w < len(a.d) {
			a.d[w] = byte(rem + '0')
		} else if rem != 0 {
			a.trunc = true
		}
		n = quo
	}

	a.nd += delta
	if a.nd >= len(a.d) {
		a.nd = len(a.d)
	}
	a.dp += delta
	trim(a)
}

// Binary shift left (k > 0) or right (k < 0).
func (a *decimal) Shift(k int) {
	switch {
	case a.nd == 0:
	// nothing to do: a == 0
	case k > 0:
		for k > maxShift {
			leftShift(a, maxShift)
			k -= maxShift
		}
		leftShift(a, uint(k))
	case k < 0:
		for k < -maxShift {
			rightShift(a, maxShift)
			k += maxShift
		}
		rightShift(a, uint(-k))
	}
}

// If we chop a at nd digits, should we round up?
func shouldRoundUp(a *decimal, nd int) bool {
	if nd < 0 || nd >= a.nd {
		return false
	}
	if a.d[nd] == '5' && nd+1 == a.nd {
		// exactly halfway - round to even
		// if we truncated, a little higher than what's recorded - always round up
		if a.trunc {
			return true
		}
		return nd > 0 && (a.d[nd-1]-'0')%2 != 0
	}
	// not halfway - digit tells all
	return a.d[nd] >= '5'
}

// Round a to nd digits (or fewer).
// If nd is zero, it means we're rounding
// just to the left of the digits, as in
// 0.09 -> 0.1.
func (a *decimal) Round(nd int) {
	if nd < 0 || nd >= a.nd {
		return
	}
	if shouldRoundUp(a, nd) {
		a.RoundUp(nd)
	} else {
		a.RoundDown(nd)
	}
}

// Round a down to nd digits (or fewer).
func (a *decimal) RoundDown(nd int) {
	if nd < 0 || nd >= a.nd {
		return
	}
	a.nd = nd
	trim(a)
}

// Round a up to nd digits (or fewer).
func (a *decimal) RoundUp(nd int) {
	if nd < 0 || nd >= a.nd {
		return
	}

	// round up
	for i := nd - 1; i >= 0; i-- {
		c := a.d[i]
		if c < '9' {
			// can stop after this digit
			a.d[i]++
			a.nd = i + 1
			return
		}
	}

	// Number is all 9s.
	// Change to single 1 with adjusted decimal point.
	a.d[0] = '1'
	a.nd = 1
	a.dp++
}

// Extract integer part, rounded appropriately.
// No guarantees about overflow.
func (a *decimal) RoundedInteger() uint64 {
	if a.dp > 20 {
		return 0xFFFFFFFFFFFFFFFF
	}
	var i int
	n := uint64(0)
	for i = 0; i < a.dp && i < a.nd; i++ {
		n = n*10 + uint64(a.d[i]-'0')
	}
	for ; i < a.dp; i++ {
		n *= 10
	}
	if shouldRoundUp(a, a.dp) {
		n++
	}
	return n
}
