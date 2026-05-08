package charset

import "testing"

func BenchmarkDecodeUTF8Direct(b *testing.B) {
	data := []byte("plain ascii text")
	b.ReportAllocs()
	for range b.N {
		_ = Decode(IDUTF8, data)
	}
}

func BenchmarkDecodeISO88591ASCII(b *testing.B) {
	data := []byte("plain ascii text")
	b.ReportAllocs()
	for range b.N {
		_ = Decode(IDISO88591, data)
	}
}

func BenchmarkDecodeISO88591Latin1(b *testing.B) {
	data := []byte{'V', 'A', 'R', 'T', 'A', 0xA0, 'C', 'R'}
	b.ReportAllocs()
	for range b.N {
		_ = Decode(IDISO88591, data)
	}
}

func BenchmarkEncodeISO88591ASCII(b *testing.B) {
	s := "plain ascii text"
	b.ReportAllocs()
	for range b.N {
		_, _ = Encode(IDISO88591, s)
	}
}

func BenchmarkEncodeISO88591Latin1(b *testing.B) {
	s := "VARTA\u00a0CR"
	b.ReportAllocs()
	for range b.N {
		_, _ = Encode(IDISO88591, s)
	}
}

func BenchmarkEncodeWIN1251(b *testing.B) {
	s := "\u041f\u0440\u0438\u0432\u0435\u0442"
	b.ReportAllocs()
	for range b.N {
		_, _ = Encode(52, s)
	}
}
