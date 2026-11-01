package jc2

import "testing"

// advertData builds manufacturer data: 01 00 03 7e 05 <pid> 00 01 <wake>
// <host> 0f 00 ...
func advertData(pid uint16, wake byte, host [6]byte) []byte {
	b := []byte{0x01, 0x00, 0x03, 0x7e, 0x05, byte(pid), byte(pid >> 8), 0x00, 0x01, wake}
	b = append(b, host[:]...)
	return append(b, 0x0f, 0, 0, 0, 0, 0, 0, 0)
}

func TestParseAdvert(t *testing.T) {
	host := [6]byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc}
	tests := []struct {
		name    string
		company uint16
		data    []byte
		want    Advert
		ok      bool
	}{
		{"left standard", CompanyID, advertData(ProductL, 0x01, [6]byte{}),
			Advert{Product: ProductL, Side: SideL, Standard: true}, true},
		{"right standard", CompanyID, advertData(ProductR, 0x01, [6]byte{}),
			Advert{Product: ProductR, Side: SideR, Standard: true}, true},
		{"right woken", CompanyID, advertData(ProductR, 0x81, [6]byte{}),
			Advert{Product: ProductR, Side: SideR, Standard: true, Wake: true}, true},
		{"left reconnecting to a console", CompanyID, advertData(ProductL, 0x01, host),
			Advert{Product: ProductL, Side: SideL, HostAddr: host}, true},
		{"minimal length", CompanyID, advertData(ProductL, 0x01, [6]byte{})[:16],
			Advert{Product: ProductL, Side: SideL, Standard: true}, true},
		{"one byte short", CompanyID, advertData(ProductL, 0x01, [6]byte{})[:15], Advert{}, false},
		{"other company", 0x004c, advertData(ProductL, 0x01, [6]byte{}), Advert{}, false},
		{"pro controller 2", CompanyID, advertData(0x2069, 0x01, [6]byte{}), Advert{}, false},
		{"switch 1 pro", CompanyID, advertData(0x2009, 0x01, [6]byte{}), Advert{}, false},
		{"other vendor", CompanyID, append([]byte{0x01, 0x00, 0x03, 0x7f, 0x05, 0x67, 0x20, 0, 1, 1}, make([]byte, 12)...), Advert{}, false},
		{"empty", CompanyID, nil, Advert{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseAdvert(tt.company, tt.data)
			if ok != tt.ok || got != tt.want {
				t.Errorf("ParseAdvert = %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func FuzzParseAdvert(f *testing.F) {
	f.Add(uint16(CompanyID), advertData(ProductL, 0x01, [6]byte{}))
	f.Add(uint16(CompanyID), advertData(ProductR, 0x81, [6]byte{1, 2, 3, 4, 5, 6}))
	f.Add(uint16(0), []byte{})
	f.Fuzz(func(t *testing.T, company uint16, data []byte) {
		a, ok := ParseAdvert(company, data)
		if !ok {
			if a != (Advert{}) {
				t.Fatalf("not ok but %+v", a)
			}
			return
		}
		if company != CompanyID || len(data) < advertLen {
			t.Fatalf("accepted company %#x with %d bytes", company, len(data))
		}
		if a.Side == 0 || SideOf(a.Product) != a.Side {
			t.Fatalf("side %v for product %#x", a.Side, a.Product)
		}
		if a.Standard != (a.HostAddr == [6]byte{}) {
			t.Fatalf("Standard %v with host %v", a.Standard, a.HostAddr)
		}
	})
}
